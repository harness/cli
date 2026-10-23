// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"

	"github.com/harness/cli/v3/pkg/auth"
)

const importArchivePath = "/gateway/ai-evals/api/v1/orgs/%s/projects/%s/evals/import-archive"

// GitSyncRecommendation is printed after a successful archive import.
// The import path does not sync Git.
const GitSyncRecommendation = "Git sync was not applied. This upload does not connect Git. To make Git the source of truth, register the repository with `harness execute eval_git_registration:register`."

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
}

// NewArchiveRequest builds the multipart POST the import-archive route accepts.
// The file part is named archive. The request Content-Type is multipart/form-data.
func NewArchiveRequest(a *auth.ResolvedAuth, payload []byte) (*http.Request, error) {
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
	endpoint := strings.TrimRight(a.APIUrl, "/") + fmt.Sprintf(importArchivePath, url.PathEscape(a.OrgID), url.PathEscape(a.ProjectID))
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
	req, err := NewArchiveRequest(a, payload)
	if err != nil {
		return "", err
	}
	if do == nil {
		do = http.DefaultClient.Do
	}
	resp, err := do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("import-archive returned %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	uiURL := ""
	if a != nil {
		uiURL = a.UIUrl
	}
	return FormatImport(a.APIUrl, uiURL, respBody)
}
