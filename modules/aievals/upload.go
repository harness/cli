// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/harness/cli/v3/pkg/auth"
	"go.yaml.in/yaml/v3"
)

const importArchivePath = "/ai-evals/api/v1/orgs/%s/projects/%s/evals/import-archive"

// GitSyncRecommendation is printed after a successful archive import.
// The import path does not sync Git.
const GitSyncRecommendation = "Git sync was not applied. This upload does not connect Git. To make Git the source of truth, register the repository with `harness execute eval_git_registration:register`."

type importMetricBinding struct {
	SourceMetricID  string `json:"source_metric_id"`
	SourceScoreName string `json:"source_score_name"`
	MetricID        string `json:"metric_id"`
	ScoreName       string `json:"score_name"`
}

type importManifest struct {
	Eval           importedResource      `json:"eval"`
	Dataset        importedResource      `json:"dataset"`
	MetricBindings []importMetricBinding `json:"metric_bindings"`
}

type importedResource struct {
	Identifier string `json:"identifier"`
	ID         string `json:"id"`
	UIPath     string `json:"ui_path"`
}

type importArchiveResponse struct {
	Agents     []importedResource `json:"agents"`
	Evals      []importedResource `json:"evals"`
	Datasets   []importedResource `json:"datasets"`
	MetricSets []importedResource `json:"metric_sets"`
	Suites     []importedResource `json:"suites"`
	Manifest   []importManifest   `json:"manifest"`
}

type ArchiveImportResult struct {
	Text     string
	Response importArchiveResponse
}

// ValidateUploadTree checks the files that BuildArchive will include without
// requiring the local execution runner. Upload is an archive/API operation;
// v1 run compatibility remains enforced by RunAgent.
func ValidateUploadTree(root string) error {
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("reading upload tree %s: %w", root, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("upload tree %s is not a directory", root)
	}

	agentFiles := 0
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".yaml" && ext != ".yml" && ext != ".jsonl" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		switch ext {
		case ".yaml", ".yml":
			if err := validateUploadYAML(path, data); err != nil {
				return err
			}
			if filepath.Base(path) == "agent.yaml" {
				agentFiles++
			}
		case ".jsonl":
			if err := validateUploadJSONL(path, data); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if agentFiles == 0 {
		return fmt.Errorf("upload tree has no agent.yaml")
	}
	return nil
}

func validateUploadYAML(path string, data []byte) error {
	var value any
	if err := yaml.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("invalid YAML in %s", path)
	}
	if filepath.Base(path) != "agent.yaml" {
		return nil
	}
	document, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("agent document %s must be a mapping", path)
	}
	if _, hasVersion := document["schema_version"]; hasVersion {
		if err := validateV1AgentDocument(document); err != nil {
			return fmt.Errorf("invalid agent document %s: %w", path, err)
		}
	}
	return nil
}

func validateUploadJSONL(path string, data []byte) error {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), MaxMemberBytes)
	line := 0
	for scanner.Scan() {
		line++
		if strings.TrimSpace(scanner.Text()) == "" {
			continue
		}
		if _, err := decodeJSONValue([]byte(scanner.Text())); err != nil {
			return fmt.Errorf("invalid JSONL in %s at line %d", path, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("reading JSONL in %s: %w", path, err)
	}
	return nil
}

// NewArchiveRequest builds the multipart POST the import-archive route accepts.
// The file part is named archive. The request Content-Type is multipart/form-data.
func NewArchiveRequest(a *auth.ResolvedAuth, payload []byte) (*http.Request, error) {
	return NewArchiveRequestWithBasePath(a, payload, "")
}

// NewArchiveRequestWithBasePath builds an archive request using an explicit
// API path prefix. An empty prefix preserves the hosted /gateway default.
// Pass "/" for a raw local API origin.
func NewArchiveRequestWithBasePath(a *auth.ResolvedAuth, payload []byte, apiBasePath string) (*http.Request, error) {
	if a == nil {
		return nil, fmt.Errorf("upload requires an authenticated profile")
	}
	if a.OrgID == "" || a.ProjectID == "" {
		return nil, fmt.Errorf("upload requires an org and project scope")
	}
	if a.APIUrl == "" {
		return nil, fmt.Errorf("upload requires an API URL")
	}
	body, contentType, err := archivePart(payload)
	if err != nil {
		return nil, err
	}
	route := fmt.Sprintf(importArchivePath, url.PathEscape(a.OrgID), url.PathEscape(a.ProjectID))
	endpoint, err := apiEndpoint(a.APIUrl, apiBasePath, route)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	if a.AccountID != "" {
		q.Set("accountIdentifier", a.AccountID)
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequest(http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	a.SetAuthHeader(req)
	req.Header.Set("Content-Type", contentType)
	if a.AccountID != "" {
		req.Header.Set("Harness-Account", a.AccountID)
	}
	return req, nil
}

func archivePart(payload []byte) ([]byte, string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	hdr := textproto.MIMEHeader{}
	hdr.Set("Content-Disposition", `form-data; name="archive"; filename="evals.zip"`)
	hdr.Set("Content-Type", "application/zip")
	part, err := w.CreatePart(hdr)
	if err != nil {
		return nil, "", err
	}
	if _, err := part.Write(payload); err != nil {
		return nil, "", err
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), w.FormDataContentType(), nil
}

// apiEndpoint joins a configured API origin, an explicit API path prefix, and
// a route without inferring a host. Hosted defaults use /gateway; callers
// targeting a raw local API must explicitly pass "/" as the prefix.
func apiEndpoint(apiURL, apiBasePath, route string) (string, error) {
	if strings.TrimSpace(apiURL) == "" {
		return "", fmt.Errorf("upload requires an API URL")
	}
	u, err := url.Parse(apiURL)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("invalid API URL; pass an origin such as https://app.harness.io or http://localhost:8000")
	}

	root := strings.TrimRight(u.Path, "/")
	prefix := strings.TrimSpace(apiBasePath)
	if strings.HasSuffix(root, "/gateway") {
		root = strings.TrimSuffix(root, "/gateway")
		if prefix == "" {
			prefix = "/gateway"
		}
	}
	if prefix == "" {
		prefix = "/gateway"
	}
	prefix = "/" + strings.Trim(prefix, "/")
	if prefix == "/" {
		prefix = ""
	}
	escapedPath := strings.TrimRight(root, "/") + prefix + "/" + strings.TrimLeft(route, "/")
	decodedPath, err := url.PathUnescape(escapedPath)
	if err != nil {
		return "", fmt.Errorf("invalid API route")
	}
	u.Path = decodedPath
	u.RawPath = escapedPath
	return u.String(), nil
}

// FormatImport prints server-returned Agent and Eval URLs plus the Git-sync recommendation.
func FormatImport(apiURL, uiURL string, body []byte) (string, error) {
	var parsed importArchiveResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("reading import response: %w", err)
	}
	if len(parsed.Agents) == 0 || len(parsed.Evals) == 0 {
		return "", fmt.Errorf("import response did not include an Agent and Eval path")
	}
	var b strings.Builder
	for _, agent := range parsed.Agents {
		if agent.UIPath == "" {
			return "", fmt.Errorf("import response did not include an Agent path")
		}
		fmt.Fprintf(&b, "Agent %s %s\n", agent.Identifier, resourceURL(apiURL, uiURL, agent.UIPath))
	}
	for _, eval := range parsed.Evals {
		if eval.UIPath == "" {
			return "", fmt.Errorf("import response did not include an Eval path")
		}
		fmt.Fprintf(&b, "Eval %s %s\n", eval.Identifier, resourceURL(apiURL, uiURL, eval.UIPath))
	}
	b.WriteString(GitSyncRecommendation)
	b.WriteByte('\n')
	return b.String(), nil
}

func formatManifestMappings(manifest []importManifest) string {
	if len(manifest) == 0 {
		return ""
	}
	var b strings.Builder
	for _, item := range manifest {
		fmt.Fprintf(&b, "Imported eval %s (%s)\n", item.Eval.Identifier, item.Eval.ID)
		for _, binding := range item.MetricBindings {
			fmt.Fprintf(
				&b,
				"Metric %s/%s -> %s/%s\n",
				binding.SourceMetricID,
				binding.SourceScoreName,
				binding.MetricID,
				binding.ScoreName,
			)
		}
	}
	return b.String()
}

func resourceURL(apiURL, uiURL, uiPath string) string {
	if strings.HasPrefix(uiPath, "http://") || strings.HasPrefix(uiPath, "https://") {
		return uiPath
	}
	base := strings.TrimRight(uiURL, "/")
	if base == "" {
		if u, err := url.Parse(apiURL); err == nil && u.Scheme != "" && u.Host != "" {
			base = u.Scheme + "://" + u.Host
		}
	}
	if !strings.HasPrefix(uiPath, "/") {
		uiPath = "/" + uiPath
	}
	return base + uiPath
}

// UploadArchive sends payload and returns the text FormatImport produces.
func UploadArchive(a *auth.ResolvedAuth, payload []byte, do func(*http.Request) (*http.Response, error)) (string, error) {
	result, err := UploadArchiveWithBasePath(a, payload, "", do)
	if err != nil {
		return "", err
	}
	return result.Text, nil
}

// UploadArchiveWithBasePath sends the archive and returns both the existing
// human-readable output and the manifest needed for optional report import.
func UploadArchiveWithBasePath(a *auth.ResolvedAuth, payload []byte, apiBasePath string, do func(*http.Request) (*http.Response, error)) (ArchiveImportResult, error) {
	req, err := NewArchiveRequestWithBasePath(a, payload, apiBasePath)
	if err != nil {
		return ArchiveImportResult{}, err
	}
	if do == nil {
		do = http.DefaultClient.Do
	}
	resp, err := do(req)
	if err != nil {
		return ArchiveImportResult{}, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return ArchiveImportResult{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ArchiveImportResult{}, fmt.Errorf("import-archive returned %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	uiURL := ""
	if a != nil {
		uiURL = a.UIUrl
	}
	text, err := FormatImport(a.APIUrl, uiURL, respBody)
	if err != nil {
		return ArchiveImportResult{}, err
	}
	var parsed importArchiveResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return ArchiveImportResult{}, fmt.Errorf("reading import response: %w", err)
	}
	return ArchiveImportResult{Text: text, Response: parsed}, nil
}
