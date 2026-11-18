// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/harness/cli/v3/pkg/auth"
	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/httpclient"
	"go.yaml.in/yaml/v3"
)

const (
	archiveImportPath      = "/ai-evals/api/v1/orgs/%s/projects/%s/evals/import-archive"
	archiveRequestTimeout  = 30 * time.Second
	maxArchiveResponseSize = 1 << 20
)

var (
	uploadIDPattern         = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	uploadIdentifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	uploadZipEpoch          = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)
)

type uploadFile struct {
	archivePath string
	data        []byte
}

type archiveImportResponse struct {
	Agents []archiveImportedResource `json:"agents"`
	Evals  []archiveImportedResource `json:"evals"`
}

type archiveImportedResource struct {
	Identifier string `json:"identifier"`
	ID         string `json:"id"`
	UIPath     string `json:"ui_path"`
}

// ValidateUploadTree validates a snapshot of the exact tree named by root.
func ValidateUploadTree(root string) error {
	_, err := collectUploadFiles(root, DefaultLimits())
	return err
}

// BuildArchive collects, validates, and archives one immutable snapshot of root.
// ZIP names use the API's canonical root regardless of the local directory name.
func BuildArchive(root string, limits Limits) ([]byte, error) {
	files, err := collectUploadFiles(root, limits)
	if err != nil {
		return nil, err
	}
	return buildArchiveFromSnapshot(files, limits)
}

func buildArchiveFromSnapshot(files []uploadFile, limits Limits) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, file := range files {
		header := &zip.FileHeader{
			Name:     file.archivePath,
			Method:   zip.Store,
			Modified: uploadZipEpoch,
		}
		header.SetMode(0o644)
		writer, err := zw.CreateHeader(header)
		if err != nil {
			return nil, fmt.Errorf("could not create the upload archive")
		}
		if _, err := writer.Write(file.data); err != nil {
			return nil, fmt.Errorf("could not write the upload archive")
		}
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("could not finalize the upload archive")
	}
	sizes := make([]int64, len(files))
	for index, file := range files {
		sizes[index] = int64(len(file.data))
	}
	if err := CheckClientLimits(normalizeUploadLimits(limits), len(files), sizes, buf.Len()); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func normalizeUploadLimits(limits Limits) Limits {
	if limits.Members <= 0 {
		return DefaultLimits()
	}
	return limits
}

func collectUploadFiles(root string, limits Limits) ([]uploadFile, error) {
	limits = normalizeUploadLimits(limits)
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("upload root is invalid")
	}
	rootInfo, err := os.Lstat(absRoot)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("upload root must be an existing directory, not a symlink")
	}

	var paths []string
	err = filepath.WalkDir(absRoot, func(fullPath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("upload tree could not be read")
		}
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".yaml" && ext != ".yml" && ext != ".jsonl" {
			return nil
		}
		info, err := os.Lstat(fullPath)
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("upload tree contains an unreadable file")
		}
		rel, err := filepath.Rel(absRoot, fullPath)
		if err != nil || !filepath.IsLocal(rel) {
			return fmt.Errorf("upload tree contains an invalid path")
		}
		canonical := path.Join(".harness", "evals", filepath.ToSlash(rel))
		if canonical == ".harness/evals" {
			return fmt.Errorf("upload tree contains an invalid path")
		}
		paths = append(paths, fullPath)
		if len(paths) > limits.Members {
			return fmt.Errorf("archive exceeds the file count limit (%d)", limits.Members)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(paths, func(i, j int) bool {
		relI, _ := filepath.Rel(absRoot, paths[i])
		relJ, _ := filepath.Rel(absRoot, paths[j])
		return filepath.ToSlash(relI) < filepath.ToSlash(relJ)
	})

	files := make([]uploadFile, 0, len(paths))
	var expanded int64
	agentCount := 0
	for _, fullPath := range paths {
		rel, err := filepath.Rel(absRoot, fullPath)
		if err != nil {
			return nil, fmt.Errorf("upload tree contains an invalid path")
		}
		data, err := readUploadFile(fullPath, limits.MemberBytes)
		if err != nil {
			return nil, err
		}
		expanded += int64(len(data))
		if expanded > MaxExpandedBytes {
			return nil, fmt.Errorf("archive exceeds the expanded-size limit (%d bytes)", MaxExpandedBytes)
		}
		canonical := path.Join(".harness", "evals", filepath.ToSlash(rel))
		if err := validateUploadMember(canonical, data); err != nil {
			return nil, err
		}
		if isUploadAgentPath(canonical) {
			agentCount++
		}
		files = append(files, uploadFile{archivePath: canonical, data: data})
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("upload tree has no YAML or JSONL files")
	}
	if agentCount == 0 {
		return nil, fmt.Errorf("upload tree has no agents/*/agent.yaml")
	}
	return files, nil
}

func readUploadFile(fullPath string, maxBytes int64) ([]byte, error) {
	before, err := os.Lstat(fullPath)
	if err != nil || !before.Mode().IsRegular() || before.Size() > maxBytes {
		return nil, fmt.Errorf("archive member exceeds the per-file limit or is not a regular file")
	}
	file, err := os.Open(fullPath)
	if err != nil {
		return nil, fmt.Errorf("upload tree contains an unreadable file")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return nil, fmt.Errorf("upload tree changed while it was being read")
	}
	after, err := os.Lstat(fullPath)
	if err != nil || !os.SameFile(opened, after) {
		return nil, fmt.Errorf("upload tree changed while it was being read")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("upload tree contains an unreadable file")
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("archive member exceeds the per-file limit")
	}
	return data, nil
}

func isUploadAgentPath(archivePath string) bool {
	rel := strings.TrimPrefix(archivePath, ".harness/evals/")
	parts := strings.Split(rel, "/")
	return len(parts) == 3 && parts[0] == "agents" && parts[1] != "" && parts[2] == "agent.yaml"
}

func validateUploadMember(archivePath string, data []byte) error {
	if !utf8.Valid(data) {
		return fmt.Errorf("archive member is not valid UTF-8")
	}
	switch strings.ToLower(filepath.Ext(archivePath)) {
	case ".yaml", ".yml":
		var value any
		if err := yaml.Unmarshal(data, &value); err != nil {
			return fmt.Errorf("archive contains invalid YAML")
		}
		if !isUploadAgentPath(archivePath) {
			return nil
		}
		document, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("agent document must be a mapping")
		}
		if _, versioned := document["schema_version"]; versioned && validateV1AgentDocument(document) != nil {
			return fmt.Errorf("agent document does not satisfy the onboarding schema")
		}
	case ".jsonl":
		scanner := bufio.NewScanner(bytes.NewReader(data))
		scanner.Buffer(make([]byte, 64*1024), MaxMemberBytes)
		for scanner.Scan() {
			line := bytes.TrimSpace(scanner.Bytes())
			if len(line) == 0 {
				continue
			}
			decoder := json.NewDecoder(bytes.NewReader(line))
			decoder.UseNumber()
			var value any
			if decoder.Decode(&value) != nil {
				return fmt.Errorf("archive contains invalid JSONL")
			}
			var trailing any
			if decoder.Decode(&trailing) != io.EOF {
				return fmt.Errorf("archive contains invalid JSONL")
			}
		}
		if scanner.Err() != nil {
			return fmt.Errorf("archive contains invalid JSONL")
		}
	}
	return nil
}

func buildValidatedUploadArchive(ctx context.Context, root string, limits Limits, runnerHome string, verify func(string, CommandFunc) (RunnerLayout, error), run CommandFunc) ([]byte, error) {
	files, err := collectUploadFiles(root, limits)
	if err != nil {
		return nil, err
	}
	if verify == nil {
		verify = VerifyRunner
	}
	layout, err := verify(runnerHome, run)
	if err != nil {
		return nil, err
	}
	if err := validateUploadSnapshot(ctx, files, layout, run); err != nil {
		return nil, err
	}
	return buildArchiveFromSnapshot(files, limits)
}

func validateUploadSnapshot(ctx context.Context, files []uploadFile, layout RunnerLayout, run CommandFunc) error {
	if layout.Python == "" || len(files) == 0 {
		return fmt.Errorf("pinned onboarding validator is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if run == nil {
		run = func(name string, args []string, env []string, dir string) (CommandResult, error) {
			return defaultCommandContext(ctx, name, args, env, dir)
		}
	}
	temporary, err := os.MkdirTemp("", "aievals-upload-")
	if err != nil {
		return fmt.Errorf("could not prepare the upload snapshot for validation")
	}
	defer os.RemoveAll(temporary)
	stagingDir, err := filepath.EvalSymlinks(temporary)
	if err != nil {
		return fmt.Errorf("could not prepare the upload snapshot for validation")
	}
	evalsRoot := filepath.Join(stagingDir, "repo", ".harness", "evals")
	for _, file := range files {
		rel := strings.TrimPrefix(file.archivePath, ".harness/evals/")
		if rel == file.archivePath || !filepath.IsLocal(filepath.FromSlash(rel)) {
			return fmt.Errorf("upload snapshot contains an invalid path")
		}
		if err := writeTreeFile(evalsRoot, filepath.FromSlash(rel), file.data); err != nil {
			return fmt.Errorf("could not prepare the upload snapshot for validation")
		}
	}
	args := ValidatorArgs(layout.Python, evalsRoot)
	result, err := run(args[0], args[1:], nil, stagingDir)
	if err != nil || result.Code != 0 {
		return fmt.Errorf("pinned onboarding validator rejected the upload snapshot")
	}
	return nil
}

// NewArchiveRequestWithBasePath builds the multipart request for the D3 route.
func NewArchiveRequestWithBasePath(a *auth.ResolvedAuth, payload []byte, apiBasePath string) (*http.Request, error) {
	return NewArchiveRequestContext(context.Background(), a, payload, apiBasePath)
}

// NewArchiveRequestContext attaches ctx to the multipart import request.
func NewArchiveRequestContext(ctx context.Context, a *auth.ResolvedAuth, payload []byte, apiBasePath string) (*http.Request, error) {
	if a == nil || a.AccountID == "" || a.OrgID == "" || a.ProjectID == "" {
		return nil, fmt.Errorf("upload requires account, org, and project scope")
	}
	if len(payload) == 0 || int64(len(payload)) > DefaultLimits().Compressed {
		return nil, fmt.Errorf("upload archive is empty or exceeds the archive limit")
	}
	endpoint, err := archiveEndpoint(a.APIUrl, apiBasePath, a.OrgID, a.ProjectID)
	if err != nil {
		return nil, err
	}
	body, contentType, err := archiveMultipartPart(payload)
	if err != nil {
		return nil, fmt.Errorf("could not encode the upload archive")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("could not construct the archive import request")
	}
	a.SetAuthHeader(req)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", contentType)
	auth.SetAccountHeader(req, a.AccountID)
	return req, nil
}

func archiveEndpoint(apiURL, apiBasePath, org, project string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(apiURL))
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("invalid API URL; configure an HTTP(S) API origin")
	}
	root := strings.TrimRight(u.EscapedPath(), "/")
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
	if strings.ContainsAny(prefix, "\\?#") || strings.Contains(prefix, "..") {
		return "", fmt.Errorf("invalid API base path")
	}
	prefix = "/" + strings.Trim(prefix, "/")
	if prefix == "/" {
		prefix = ""
	}
	route := fmt.Sprintf(archiveImportPath, url.PathEscape(org), url.PathEscape(project))
	escapedPath := strings.TrimRight(root, "/") + prefix + route
	decodedPath, err := url.PathUnescape(escapedPath)
	if err != nil {
		return "", fmt.Errorf("invalid API path")
	}
	u.Path = decodedPath
	u.RawPath = escapedPath
	return u.String(), nil
}

func archiveMultipartPart(payload []byte) ([]byte, string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="archive"; filename="evals.zip"`)
	header.Set("Content-Type", "application/zip")
	part, err := writer.CreatePart(header)
	if err != nil {
		return nil, "", err
	}
	if _, err := part.Write(payload); err != nil {
		return nil, "", err
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return body.Bytes(), writer.FormDataContentType(), nil
}

func UploadArchiveWithBasePath(a *auth.ResolvedAuth, payload []byte, apiBasePath string, do func(*http.Request) (*http.Response, error)) (string, error) {
	return UploadArchiveContext(context.Background(), a, payload, apiBasePath, do)
}

// UploadArchiveContext sends one bounded archive and returns only validated, in-scope links.
func UploadArchiveContext(ctx context.Context, a *auth.ResolvedAuth, payload []byte, apiBasePath string, do func(*http.Request) (*http.Response, error)) (string, error) {
	req, err := NewArchiveRequestContext(ctx, a, payload, apiBasePath)
	if err != nil {
		return "", err
	}
	if do == nil {
		client := &http.Client{
			Timeout: archiveRequestTimeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
		do = func(req *http.Request) (*http.Response, error) {
			return httpclient.DoWithProxyCheck(client, req)
		}
	}
	resp, err := do(req)
	if err != nil {
		return "", fmt.Errorf("archive upload request failed; check the API URL, network, and proxy configuration")
	}
	if resp == nil || resp.Body == nil {
		return "", fmt.Errorf("archive upload returned no response")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxArchiveResponseSize+1))
	if err != nil || len(body) > maxArchiveResponseSize {
		return "", fmt.Errorf("archive upload response could not be read safely")
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("archive upload returned HTTP %d", resp.StatusCode)
	}
	return formatArchiveImport(a, body)
}

func formatArchiveImport(a *auth.ResolvedAuth, body []byte) (string, error) {
	if a == nil || a.AccountID == "" || a.OrgID == "" || a.ProjectID == "" {
		return "", fmt.Errorf("archive response cannot be linked without account, org, and project scope")
	}
	var imported archiveImportResponse
	if err := json.Unmarshal(body, &imported); err != nil || len(imported.Agents) == 0 || len(imported.Evals) == 0 {
		return "", fmt.Errorf("archive import response did not include valid Agent and Eval links")
	}
	origin, err := uploadUIOrigin(a)
	if err != nil {
		return "", err
	}
	var result strings.Builder
	for _, agent := range imported.Agents {
		link, err := scopedImportLink(origin, a, "targets", agent)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&result, "Agent %s %s\n", agent.Identifier, link)
	}
	for _, eval := range imported.Evals {
		link, err := scopedImportLink(origin, a, "evals", eval)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&result, "Eval %s %s\n", eval.Identifier, link)
	}
	return result.String(), nil
}

func uploadUIOrigin(a *auth.ResolvedAuth) (*url.URL, error) {
	base := strings.TrimSpace(a.UIUrl)
	if base == "" {
		base = strings.TrimSpace(a.APIUrl)
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("archive import response has no safe UI origin")
	}
	return &url.URL{Scheme: u.Scheme, Host: u.Host}, nil
}

func scopedImportLink(origin *url.URL, a *auth.ResolvedAuth, kind string, resource archiveImportedResource) (string, error) {
	if !uploadIdentifierPattern.MatchString(resource.Identifier) || isCredentialName(resource.Identifier) || secretShaped(resource.Identifier) || uploadIDPattern.FindString(resource.ID) != resource.ID || resource.ID == "" {
		return "", fmt.Errorf("archive import response contained an invalid resource identity")
	}
	expected := []string{"account", a.AccountID, "all", "ai-evals", "orgs", a.OrgID, "projects", a.ProjectID, kind, resource.ID}
	if !sameUploadPath(resource.UIPath, expected) {
		return "", fmt.Errorf("archive import response contained a path outside the active scope")
	}
	encoded := make([]string, len(expected))
	for index, segment := range expected {
		encoded[index] = url.PathEscape(segment)
	}
	escapedPath := "/" + strings.Join(encoded, "/")
	decodedPath, err := url.PathUnescape(escapedPath)
	if err != nil {
		return "", fmt.Errorf("archive import response contained an invalid resource path")
	}
	link := &url.URL{Scheme: origin.Scheme, Host: origin.Host, Path: decodedPath, RawPath: escapedPath}
	return link.String(), nil
}

func sameUploadPath(rawPath string, expected []string) bool {
	if rawPath == "" || !strings.HasPrefix(rawPath, "/") || strings.HasPrefix(rawPath, "//") || strings.ContainsAny(rawPath, "?#\\") {
		return false
	}
	parsed, err := url.ParseRequestURI(rawPath)
	if err != nil || parsed.IsAbs() || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	segments := strings.Split(strings.TrimPrefix(parsed.EscapedPath(), "/"), "/")
	if len(segments) != len(expected) {
		return false
	}
	for index, escaped := range segments {
		segment, err := url.PathUnescape(escaped)
		if err != nil || segment == "" || segment == "." || segment == ".." || strings.ContainsAny(segment, "/\\") || segment != expected[index] {
			return false
		}
	}
	return true
}

func uploadWorkflow(ctx *cmdctx.Ctx) error {
	if ctx == nil || ctx.Auth == nil {
		return fmt.Errorf("upload requires an authenticated profile")
	}
	root := cmdctx.GetString(ctx.FlagValues, "root")
	if root == "" {
		root = ".harness/evals"
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("upload root is invalid")
	}
	run := func(name string, args []string, env []string, dir string) (CommandResult, error) {
		return defaultCommandContext(ctx.Context, name, args, env, dir)
	}
	payload, err := buildValidatedUploadArchive(ctx.Context, root, DefaultLimits(), RunnerHome(), VerifyRunner, run)
	if err != nil {
		return err
	}

	apiAuth := ctx.ScopedAuth()
	if apiURL := cmdctx.GetString(ctx.FlagValues, "api-url"); apiURL != "" {
		scoped := *apiAuth
		scoped.APIUrl = apiURL
		apiAuth = &scoped
	}
	text, err := UploadArchiveContext(
		ctx.Context,
		apiAuth,
		payload,
		cmdctx.GetString(ctx.FlagValues, "api-base-path"),
		nil,
	)
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(os.Stdout, text)
	return err
}
