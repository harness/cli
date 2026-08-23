// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package rt

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/harness/cli/pkg/client"
	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/console"
)

const (
	encodeScriptBodyFnID    = "encode_script"
	formatScriptID          = "format_script"
	resolveScriptRevisionID = "resolve_script_revision"

	revisionScan = 100
)

// The API stores scripts base64-encoded, which the framework's -f handling cannot produce.
func encodeScriptBody(ctx *cmdctx.Ctx) (any, error) {
	content, err := cmdctx.SlurpInputFile(ctx.FlagValues)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("%s is empty, so there is nothing to upload", cmdctx.GetString(ctx.FlagValues, "file"))
	}
	body := map[string]any{
		"scriptContent": base64.StdEncoding.EncodeToString([]byte(content)),
	}
	if description := cmdctx.GetString(ctx.FlagValues, "description"); description != "" {
		body["description"] = description
	}
	return body, nil
}

// Writes the script back exactly as uploaded, so "get loadtest_script <id> -o x.jmx" is a download.
func formatScript(w io.Writer, d cmdctx.DataAccessor) error {
	encoded := d.GetString("it.scriptContent")
	if encoded == "" {
		return errors.New("this load test has no stored script: tests that run from a container image carry their script inside the image")
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return fmt.Errorf("decoding the stored script: %w", err)
	}
	// Bundles are zip archives; painting one onto a terminal is never what was wanted.
	if d.GetBool("it.isBundle") && w == os.Stdout && console.IsStdoutTTY() {
		return fmt.Errorf("this revision is %s: save it with -o bundle.zip, or redirect stdout", bundleKind(d))
	}
	_, err = w.Write(decoded)
	return err
}

func bundleKind(d cmdctx.DataAccessor) string {
	if main := d.GetString("it.bundleMainFile"); main != "" {
		return "a zip workspace, with " + main + " as its main plan"
	}
	return "a zip workspace"
}

// The console numbers revisions 1, 2, 3, but the route keys on the identity — look a bare number up.
func resolveScriptRevision(ctx *cmdctx.Ctx, raw string) (string, error) {
	number, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		// Not a number, so it is already an identifier.
		return raw, nil
	}
	if ctx.Id == "" {
		return "", errors.New("a load test id is needed to look up a revision by number")
	}

	qp := scopeParams(ctx)
	qp["limit"] = strconv.Itoa(revisionScan)
	resp, _, err := client.New(ctx).Get(basePath+"/load-tests/"+url.PathEscape(ctx.Id)+"/script/revisions", qp)
	if err != nil {
		return "", fmt.Errorf("reading the script revisions of load test %q: %w", ctx.Id, err)
	}

	for _, item := range itemsOf(resp) {
		m := asMap(item)
		if int(floatField(m, "revisionNumber")) != number {
			continue
		}
		if identity := stringField(m, "identity"); identity != "" {
			return identity, nil
		}
	}
	return "", fmt.Errorf("load test %q has no script revision %d among its most recent %d; list them with: harness list loadtest_script:revisions %s",
		ctx.Id, number, revisionScan, ctx.Id)
}
