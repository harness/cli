// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package code

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/harness/cli/v3/pkg/client"
	"github.com/harness/cli/v3/pkg/cmdctx"
)

const uploadRepoWorkflowID = "execute_repo_upload"

const progressMinBytes = 1 << 20

var (
	imageExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".svg": true}
	videoExts = map[string]bool{".mp4": true, ".mov": true, ".webm": true}
)

func supportedExts() string {
	var exts []string
	for e := range imageExts {
		exts = append(exts, e)
	}
	for e := range videoExts {
		exts = append(exts, e)
	}
	sort.Strings(exts)
	return strings.Join(exts, ", ")
}

// checkUploadable fails before any request when the file can't or shouldn't be uploaded.
func checkUploadable(path string) (os.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", path, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%s is a directory, expected a file", path)
	}
	ext := strings.ToLower(filepath.Ext(path))
	if !imageExts[ext] && !videoExts[ext] {
		return nil, fmt.Errorf("unsupported file type %q for %s (supported: %s)", ext, path, supportedExts())
	}
	if info.Size() == 0 {
		return nil, fmt.Errorf("%s is empty", path)
	}
	return info, nil
}

// uploadSnippet returns the Markdown to paste into a PR description: an inline image
// for images, a plain link for videos.
func uploadSnippet(name, link string) string {
	label := strings.NewReplacer("[", `\[`, "]", `\]`).Replace(name)
	if videoExts[strings.ToLower(filepath.Ext(name))] {
		return fmt.Sprintf("[%s](%s)", label, link)
	}
	return fmt.Sprintf("![%s](%s)", label, link)
}

// uploadURL is where the uploaded file can be fetched from.
func uploadURL(cc *cmdctx.Ctx, repo, filePath string) string {
	q := url.Values{"accountIdentifier": {cc.Auth.AccountID}}
	for k, v := range scopeQueryParams(cc) {
		if v != "" {
			q.Set(k, v)
		}
	}
	segs := strings.Split(filePath, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return fmt.Sprintf("%s/code/api/v1/repos/%s/uploads/%s?%s",
		strings.TrimRight(cc.Auth.APIUrl, "/"), url.PathEscape(repo), strings.Join(segs, "/"), q.Encode())
}

// repoFileSizeLimit returns the repo's file size limit in bytes, or 0 when there is
// none or it can't be read; the server still enforces the real limit.
func repoFileSizeLimit(cc *cmdctx.Ctx, c *client.Client, repo string) int64 {
	res, _, err := c.Get("/code/api/v1/repos/"+url.PathEscape(repo)+"/settings/general", scopeQueryParams(cc))
	if err != nil {
		return 0
	}
	m, _ := res.(map[string]any)
	limit, _ := m["file_size_limit"].(float64)
	return int64(limit)
}

type progressReader struct {
	r     io.Reader
	total int64
	read  int64
	name  string
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.read += int64(n)
	fmt.Fprintf(os.Stderr, "\rUploading %s: %d%%", p.name, p.read*100/p.total)
	if err == io.EOF {
		fmt.Fprintln(os.Stderr)
	}
	return n, err
}

// uploadRepoWorkflow implements "execute repo:upload <repo_id> -f <file>".
func uploadRepoWorkflow(ctx *cmdctx.Ctx) error {
	path := cmdctx.GetString(ctx.FlagValues, "file")
	if path == "" {
		return errors.New("--file is required")
	}
	info, err := checkUploadable(path)
	if err != nil {
		return err
	}

	c := client.New(ctx)
	if ctx.RequestPreview == nil {
		if limit := repoFileSizeLimit(ctx, c, ctx.Id); limit > 0 && info.Size() > limit {
			return fmt.Errorf("%s is %d bytes, over the repository file size limit of %d bytes", path, info.Size(), limit)
		}
	}

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("cannot read %s: %w", path, err)
	}
	defer f.Close()

	var body io.Reader = f
	if ctx.IsPty && info.Size() >= progressMinBytes {
		body = &progressReader{r: f, total: info.Size(), name: filepath.Base(path)}
	}

	res, _, err := c.DoRequest(client.Request{
		Method:          "POST",
		Path:            "/code/api/v1/repos/" + url.PathEscape(ctx.Id) + "/uploads",
		QueryParams:     scopeQueryParams(ctx),
		BodyStream:      body,
		BodyLength:      info.Size(),
		BodyContentType: "application/octet-stream",
	})
	if errors.Is(err, client.ErrRequestPreviewed) {
		return nil
	}
	if err != nil {
		return err
	}
	m, _ := res.(map[string]any)
	filePath, _ := m["file_path"].(string)
	if filePath == "" {
		return errors.New("upload succeeded but the response had no file_path")
	}

	link := uploadURL(ctx, ctx.Id, filePath)
	fmt.Printf("file_path: %s\nurl: %s\nsnippet: %s\n", filePath, link, uploadSnippet(filepath.Base(path), link))
	return nil
}
