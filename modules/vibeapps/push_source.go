// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package vibeapps

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/harness/cli/pkg/client"
	"github.com/harness/cli/pkg/cmdctx"
)

const pushVibeappSourceWorkflowID = "push_vibeapp_source"

const sourcePollInterval = 2 * time.Second

type createSourceResponse struct {
	Source struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"source"`
	Upload struct {
		UploadID string             `json:"uploadId"`
		Files    []uploadFileTarget `json:"files"`
	} `json:"upload"`
}

type uploadFileTarget struct {
	Path       string            `json:"path"`
	ObjectPath string            `json:"objectPath"`
	UploadURL  string            `json:"uploadUrl"`
	Method     string            `json:"method"`
	Headers    map[string]string `json:"headers"`
}

type sourceStatusResponse struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	StatusDetail string `json:"statusDetail"`
}

// pushVibeappSourceWorkflow implements "push vibeapp_source <local.zip>": creates a
// source (type upload), PUTs the zip's bytes to the returned upload target(s), then
// polls until the source status is "ready".
func pushVibeappSourceWorkflow(ctx *cmdctx.Ctx) error {
	if len(ctx.Args) == 0 {
		return fmt.Errorf("push vibeapp_source requires a local zip path")
	}
	localFile := ctx.Args[0]

	f, err := os.Open(localFile)
	if err != nil {
		return fmt.Errorf("opening %q: %w", localFile, err)
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat %q: %w", localFile, err)
	}

	sum := md5.New()
	if _, err := io.Copy(sum, f); err != nil {
		return fmt.Errorf("reading %q: %w", localFile, err)
	}
	md5Hex := hex.EncodeToString(sum.Sum(nil))

	name := cmdctx.GetString(ctx.FlagValues, "name")
	if name == "" {
		base := filepath.Base(localFile)
		name = strings.TrimSuffix(base, filepath.Ext(base))
	}
	appID := cmdctx.GetString(ctx.FlagValues, "app")
	description := cmdctx.GetString(ctx.FlagValues, "description")

	body := map[string]any{
		"name": name,
		"source": map[string]any{
			"type": "upload",
			"files": []map[string]any{
				{
					"path":        filepath.Base(localFile),
					"sizeBytes":   fi.Size(),
					"contentType": "application/zip",
					"md5":         md5Hex,
				},
			},
		},
	}
	if description != "" {
		body["description"] = description
	}

	path := fmt.Sprintf(apiPrefix+"/api/v1/spaces/%s/sources", sentinelSpaceID)
	if appID != "" {
		path = fmt.Sprintf(apiPrefix+"/api/v1/apps/%s/sources", appID)
	}

	fmt.Fprintf(os.Stderr, "Creating source %q (%s) ...\n", name, formatBytes(fi.Size()))
	raw, _, err := client.New(ctx).Post(path, nil, body)
	if err != nil {
		return fmt.Errorf("creating source: %w", err)
	}
	var created createSourceResponse
	if err := decodeInto(raw, &created); err != nil {
		return fmt.Errorf("parsing create-source response: %w", err)
	}
	if created.Source.ID == "" {
		return fmt.Errorf("create-source response had no source id")
	}

	for _, target := range created.Upload.Files {
		fmt.Fprintf(os.Stderr, "Uploading %s ...\n", target.Path)
		if err := putUploadFile(ctx, target, localFile); err != nil {
			return fmt.Errorf("uploading %s: %w", target.Path, err)
		}
	}

	fmt.Fprintln(os.Stderr, "Waiting for source to become ready ...")
	status, err := pollSourceReady(ctx, created.Source.ID)
	if err != nil {
		return err
	}

	fmt.Printf("\nSource ready: %s (%s)\n", status.ID, status.Status)
	if appID != "" {
		fmt.Printf("Intaken as a new version on app %s.\n", appID)
	} else {
		fmt.Printf("\nTo create an app from it:\nharness create vibeapp %s --source-id %s\n", name, status.ID)
	}
	return nil
}

func putUploadFile(ctx *cmdctx.Ctx, target uploadFileTarget, localFile string) error {
	f, err := os.Open(localFile)
	if err != nil {
		return fmt.Errorf("opening %q: %w", localFile, err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat %q: %w", localFile, err)
	}

	method := target.Method
	if method == "" {
		method = http.MethodPut
	}
	req, err := http.NewRequestWithContext(ctx.Context, method, target.UploadURL, f)
	if err != nil {
		return fmt.Errorf("building upload request: %w", err)
	}
	req.ContentLength = fi.Size()
	for k, v := range target.Headers {
		req.Header.Set(k, v)
	}
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/zip")
	}
	// The upload target's own headers (or a GCS pre-signed query string) already carry
	// whatever auth that URL needs; only add ours when the URL points back at our own API
	// host, since GCS would reject an unsigned extra header on a signed URL.
	if sameHost(target.UploadURL, ctx.Auth.APIUrl) {
		ctx.Auth.SetAuthHeader(req)
	}

	httpClient := &http.Client{Timeout: 10 * time.Minute}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

func sameHost(rawURL, apiURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	a, err := url.Parse(apiURL)
	if err != nil {
		return false
	}
	return u.Host == a.Host
}

// pollSourceReady polls GET /api/v1/sources/{id} until the source reaches a terminal
// status ("ready" or "failed"), or the command context is canceled.
func pollSourceReady(ctx *cmdctx.Ctx, sourceID string) (*sourceStatusResponse, error) {
	ticker := time.NewTicker(sourcePollInterval)
	defer ticker.Stop()

	var previous string
	for {
		raw, _, err := client.New(ctx).Get(apiPrefix+"/api/v1/sources/"+sourceID, nil)
		if err != nil {
			return nil, fmt.Errorf("polling source %s: %w", sourceID, err)
		}
		var status sourceStatusResponse
		if err := decodeInto(raw, &status); err != nil {
			return nil, fmt.Errorf("parsing source status: %w", err)
		}
		if status.Status != previous {
			fmt.Fprintf(os.Stderr, "  status: %s\n", status.Status)
			previous = status.Status
		}
		switch status.Status {
		case "ready":
			return &status, nil
		case "failed":
			detail := status.StatusDetail
			if detail == "" {
				detail = "source intake failed"
			}
			return nil, fmt.Errorf("source %s failed: %s", sourceID, detail)
		}

		select {
		case <-ctx.Context.Done():
			return nil, fmt.Errorf("canceled while waiting for source %s to become ready (last status: %s)", sourceID, status.Status)
		case <-ticker.C:
		}
	}
}

func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
