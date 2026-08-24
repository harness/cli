// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package rt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Returns what was written, since the point of the command is the document on disk.
func exportedYaml(t *testing.T, id string, routes map[string]any) (string, *[]call) {
	t.Helper()
	ctx, calls := apiCtx(t, routes)
	ctx.Id = id
	ctx.FormatFlags.OutFile = filepath.Join(t.TempDir(), "template.yaml")

	if err := exportTemplateYaml(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	written, err := os.ReadFile(ctx.FormatFlags.OutFile)
	if err != nil {
		t.Fatalf("nothing was written: %v", err)
	}
	return string(written), calls
}

func TestExportTemplateYaml(t *testing.T) {
	const doc = "apiVersion: v1\nkind: LoadTestTemplate\nname: checkout\n"
	got, _ := exportedYaml(t, "checkout", map[string]any{
		api("/load-test-templates/checkout/yaml"): rawResponse(doc),
	})
	if got != doc {
		t.Errorf("got %q, want the document unchanged", got)
	}
}

func TestExportTemplateYamlEndsWithANewline(t *testing.T) {
	got, _ := exportedYaml(t, "checkout", map[string]any{
		api("/load-test-templates/checkout/yaml"): rawResponse("name: checkout"),
	})
	if got != "name: checkout\n" {
		t.Errorf("got %q, want a trailing newline added", got)
	}
}

func TestExportTemplateYamlPassesRevisionAndHub(t *testing.T) {
	ctx, calls := apiCtx(t, map[string]any{
		api("/load-test-templates/checkout/yaml"): rawResponse("name: checkout\n"),
	})
	ctx.Id = "checkout"
	ctx.FormatFlags.OutFile = filepath.Join(t.TempDir(), "template.yaml")
	ctx.FlagValues["revision"] = "3"
	ctx.FlagValues["hub"] = "chaoshub198x"

	if err := exportTemplateYaml(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c, ok := findCall(calls, "GET", api("/load-test-templates/checkout/yaml"))
	if !ok {
		t.Fatal("the template was never read")
	}
	if c.query.Get("revision") != "3" {
		t.Errorf("revision = %q, want --revision carried through", c.query.Get("revision"))
	}
	if c.query.Get("hubIdentity") != "chaoshub198x" {
		t.Errorf("hubIdentity = %q, want --hub carried through", c.query.Get("hubIdentity"))
	}
	// An absent flag must be omitted, not sent empty: "" is a hub name to the route.
	if _, present := c.query["scope"]; present {
		t.Error("only the flags that were given belong in the request")
	}
}

func TestExportTemplateYamlOmitsAbsentFlags(t *testing.T) {
	_, calls := exportedYaml(t, "checkout", map[string]any{
		api("/load-test-templates/checkout/yaml"): rawResponse("name: checkout\n"),
	})
	c, _ := findCall(calls, "GET", api("/load-test-templates/checkout/yaml"))
	for _, key := range []string{"revision", "hubIdentity"} {
		if _, present := c.query[key]; present {
			t.Errorf("%s was sent empty; an absent flag should be left out", key)
		}
	}
}

func TestExportTemplateYamlNeedsAnID(t *testing.T) {
	ctx, _ := apiCtx(t, nil)
	err := exportTemplateYaml(ctx)
	if err == nil || !strings.Contains(err.Error(), "<template-id>") {
		t.Fatalf("expected the missing-id message, got %v", err)
	}
}

func TestExportTemplateYamlRefusesAnEmptyDocument(t *testing.T) {
	ctx, _ := apiCtx(t, map[string]any{
		api("/load-test-templates/checkout/yaml"): rawResponse("  \n\t\n"),
	})
	ctx.Id = "checkout"

	err := exportTemplateYaml(ctx)
	if err == nil || !strings.Contains(err.Error(), "empty document") {
		t.Fatalf("expected an empty export to be refused, got %v", err)
	}
}

func TestExportTemplateYamlReportsAnAPIError(t *testing.T) {
	ctx, _ := apiCtx(t, nil) // every route 404s
	ctx.Id = "checkout"

	err := exportTemplateYaml(ctx)
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("expected the status reported, got %v", err)
	}
}
