// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package code

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/harness/cli/v3/pkg/client"
	"github.com/harness/cli/v3/pkg/cmdctx"
)

// createPRCommentBodyFn builds the comment create body.
// -f/--file (or -f -) supplies the comment text as plain text.
// --reply-to sets parent_id for threaded replies.
func createPRCommentBodyFn(ctx *cmdctx.Ctx) (any, error) {
	location, err := prCommentLocation(ctx)
	if err != nil {
		return nil, err
	}

	text, err := cmdctx.SlurpInputFile(ctx.FlagValues)
	if err != nil {
		return nil, fmt.Errorf("comment text: use -f <file> or -f - to read from stdin")
	}
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return nil, fmt.Errorf("comment text is empty")
	}

	body := map[string]any{"text": text}

	if replyTo := cmdctx.GetInt(ctx.FlagValues, "reply-to"); replyTo != 0 {
		body["parent_id"] = replyTo
	}

	if location != nil {
		if len(ctx.IdParts) < 2 {
			return nil, fmt.Errorf("expected <repo_id>/<pr_number>")
		}
		params := map[string]string{
			"accountIdentifier": ctx.Auth.AccountID,
			"orgIdentifier":     ctx.Auth.OrgID,
			"projectIdentifier": ctx.Auth.ProjectID,
		}
		raw, _, err := client.New(ctx).Get(fmt.Sprintf("/code/api/v1/repos/%s/pullreq/%s", ctx.IdParts[0], ctx.IdParts[1]), params)
		if err != nil {
			return nil, fmt.Errorf("fetching PR to get commit SHAs: %w", err)
		}
		pr, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("unexpected PR response type")
		}
		// The PR exposes its diff base, not the target branch's tip SHA.
		for _, field := range []struct{ response, request string }{
			{"source_sha", "source_commit_sha"},
			{"merge_base_sha", "target_commit_sha"},
		} {
			sha, _ := pr[field.response].(string)
			if sha == "" {
				return nil, fmt.Errorf("PR response missing %s", field.response)
			}
			body[field.request] = sha
		}
		for key, value := range location {
			body[key] = value
		}
	}

	return body, nil
}

func prCommentLocation(ctx *cmdctx.Ctx) (map[string]any, error) {
	inline := false
	for _, name := range []string{"path", "line", "end-line", "side"} {
		inline = inline || ctx.ProvidedFlags[name] || cmdctx.GetString(ctx.FlagValues, name) != ""
	}
	if !inline {
		return nil, nil
	}
	if ctx.ProvidedFlags["reply-to"] || cmdctx.GetString(ctx.FlagValues, "reply-to") != "" {
		return nil, fmt.Errorf("inline location flags cannot be combined with --reply-to; replies inherit the thread's location")
	}
	path := cmdctx.GetString(ctx.FlagValues, "path")
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("inline comments require --path and --line")
	}
	line, err := strconv.Atoi(cmdctx.GetString(ctx.FlagValues, "line"))
	if err != nil || line <= 0 {
		return nil, fmt.Errorf("--line must be a positive integer")
	}
	end := line
	if value := cmdctx.GetString(ctx.FlagValues, "end-line"); value != "" || ctx.ProvidedFlags["end-line"] {
		end, err = strconv.Atoi(value)
		if err != nil || end <= 0 {
			return nil, fmt.Errorf("--end-line must be a positive integer")
		}
		if end < line {
			return nil, fmt.Errorf("--end-line must be greater than or equal to --line")
		}
	}
	side := cmdctx.GetString(ctx.FlagValues, "side")
	if side == "" && !ctx.ProvidedFlags["side"] {
		side = "new"
	}
	if side != "new" && side != "old" {
		return nil, fmt.Errorf("--side must be new or old")
	}
	return map[string]any{
		"path":           path,
		"line_start":     line,
		"line_end":       end,
		"line_start_new": side == "new",
		"line_end_new":   side == "new",
	}, nil
}
