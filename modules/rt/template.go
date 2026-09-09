// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package rt

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"

	"github.com/harness/cli/v3/pkg/client"
	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/format"
)

const exportTemplateYamlWorkflowID = "export_template_yaml"

// A workflow, not an endpoint: the route answers with YAML, which the shared client's
// unconditional json.Unmarshal cannot decode, so this goes through client.DoRaw.
func exportTemplateYaml(ctx *cmdctx.Ctx) error {
	if ctx.Id == "" {
		return errors.New("get loadtest_template:yaml requires a <template-id>")
	}

	qp := scopeParams(ctx)
	if revision := cmdctx.GetString(ctx.FlagValues, "revision"); revision != "" {
		qp["revision"] = revision
	}
	if hub := cmdctx.GetString(ctx.FlagValues, "hub"); hub != "" {
		qp["hubIdentity"] = hub
	}

	resp, err := client.New(ctx).DoRaw(client.Request{
		Method:      "GET",
		Path:        basePath + "/load-test-templates/" + url.PathEscape(ctx.Id) + "/yaml",
		QueryParams: qp,
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading the exported template: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("API error %d: %s", resp.StatusCode, client.APIErrorMessage(resp.StatusCode, body))
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return fmt.Errorf("template %q exported as an empty document", ctx.Id)
	}

	w, closeW, err := format.OpenWriter(ctx.FormatFlags.OutFile)
	if err != nil {
		return err
	}
	defer closeW()

	if !bytes.HasSuffix(body, []byte("\n")) {
		body = append(body, '\n')
	}
	_, err = w.Write(body)
	return err
}
