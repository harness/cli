// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package code

import (
	"errors"
	"fmt"
	"io"
	"net/url"

	"github.com/harness/cli/v3/pkg/client"
	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/format"
	"github.com/harness/cli/v3/pkg/registry"
)

const getPRDiffWorkflowID = "get_pr_diff"

// The diff route only returns the unified patch as text/plain, which the shared client's
// unconditional json.Unmarshal cannot decode, so --text goes through client.DoRaw.
func getPRDiffWorkflow(ctx *cmdctx.Ctx) error {
	if !cmdctx.GetBool(ctx.FlagValues, "text") {
		if ctx.Ids["path"] == "" {
			return errors.New("get pr_diff needs a file path (<repo_id>/<pr_number>/<path>), or --text for the whole diff; use list pr_diff to see all changed files")
		}
		getSpec := ctx.Resolver.GetSpec("get", "pr_diff")
		if getSpec == nil || getSpec.Endpoint == nil {
			return errors.New("get pr_diff command spec not found")
		}
		_, err := registry.RunEndpoint(ctx, getSpec.Endpoint)
		return err
	}

	qp := map[string]string{}
	if ctx.Auth != nil {
		qp["orgIdentifier"] = ctx.Auth.OrgID
		qp["projectIdentifier"] = ctx.Auth.ProjectID
	}
	qp["path"] = ctx.Ids["path"]

	resp, err := client.New(ctx).DoRaw(client.Request{
		Method:      "GET",
		Path:        "/code/api/v1/repos/" + url.PathEscape(ctx.Ids["repo"]) + "/pullreq/" + url.PathEscape(ctx.Ids["pr"]) + "/diff",
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
	_, err = w.Write(body)
	return err
}
