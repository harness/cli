// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package code

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/harness/cli/v3/pkg/client"
	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/console"
	"github.com/harness/cli/v3/pkg/format"
	"github.com/harness/cli/v3/pkg/registry"
)

const (
	getPRDiffWorkflowID     = "get_pr_diff"
	getPRDiffTextWorkflowID = "get_pr_diff_text"
)

func getPRDiffWorkflow(ctx *cmdctx.Ctx) error {
	if !cmdctx.GetBool(ctx.FlagValues, "text") {
		getSpec := ctx.Resolver.GetSpec("get", "pr_diff")
		if getSpec == nil || getSpec.Endpoint == nil {
			return errors.New("get pr_diff command spec not found")
		}
		_, err := registry.RunEndpoint(ctx, getSpec.Endpoint)
		return err
	}
	return writePRDiffText(ctx, ctx.Ids["repo_id"], ctx.Ids["pr_number"], ctx.Ids["path"])
}

func getPRDiffTextWorkflow(ctx *cmdctx.Ctx) error {
	if len(ctx.IdParts) != 2 || ctx.IdParts[0] == "" || ctx.IdParts[1] == "" {
		return errors.New("expected <repo_id>/<pr_number>")
	}
	return writePRDiffText(ctx, ctx.IdParts[0], ctx.IdParts[1], "")
}

// The diff route only returns the unified patch as text/plain, which the shared client's
// unconditional json.Unmarshal cannot decode, so it goes through client.DoRaw. An empty
// path asks for every file of the pull request.
func writePRDiffText(ctx *cmdctx.Ctx, repoID, prNumber, path string) error {
	if f := ctx.FormatFlags.Format; f != "" && f != "text" {
		return fmt.Errorf("the raw diff is text-only and cannot be output as %s; use `harness list pr_diff` for structured output", f)
	}

	qp := map[string]string{"path": path}
	if ctx.Auth != nil {
		qp["orgIdentifier"] = ctx.Auth.OrgID
		qp["projectIdentifier"] = ctx.Auth.ProjectID
	}

	resp, err := client.New(ctx).DoRaw(client.Request{
		Method:      "GET",
		Path:        "/code/api/v1/repos/" + url.PathEscape(repoID) + "/pullreq/" + url.PathEscape(prNumber) + "/diff",
		QueryParams: qp,
		Headers:     map[string]string{"Accept": "text/plain"},
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading the diff: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("API error %d: %s", resp.StatusCode, client.APIErrorMessage(resp.StatusCode, body))
	}

	w, err := format.OpenWriter(ctx.FormatFlags.OutFile)
	if err != nil {
		return err
	}
	if ctx.FormatFlags.OutFile == "" {
		body = []byte(colorUnifiedDiff(string(body), console.WithColor))
	}
	_, err = w.Write(body)
	return err
}

// colorUnifiedDiff tints additions green, deletions red and hunk headers cyan. A "---"/"+++"
// line is only a file header before a file's first hunk; inside a hunk it is a removed or
// added line whose own content starts with "--" or "++".
func colorUnifiedDiff(diff string, paint func(console.Color, string) string) string {
	lines := strings.Split(diff, "\n")
	inHunk := false
	for i, l := range lines {
		switch {
		case strings.HasPrefix(l, "diff --git "):
			inHunk = false
		case strings.HasPrefix(l, "@@"):
			inHunk = true
			lines[i] = paint(console.ColorCyan, l)
		case inHunk && strings.HasPrefix(l, "+"):
			lines[i] = paint(console.ColorGreen, l)
		case inHunk && strings.HasPrefix(l, "-"):
			lines[i] = paint(console.ColorRed, l)
		}
	}
	return strings.Join(lines, "\n")
}
