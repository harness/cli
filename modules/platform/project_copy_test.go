// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package platform

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harness/cli/v3/modules/platform/projectcopy"
	"github.com/harness/cli/v3/pkg/auth"
	"github.com/harness/cli/v3/pkg/cmdctx"
)

func copyContext() *cmdctx.Ctx {
	return &cmdctx.Ctx{Auth: &auth.ResolvedAuth{AccountID: "fixture_account", OrgID: "fixture_org", ProjectID: "ignored_profile_project"}, MigrateFrom: "source", MigrateTo: "destination", FlagValues: map[string]any{"resources": "cd-core"}, ProvidedFlags: map[string]bool{}}
}

func TestProjectCopyRejectsUnavailableWorkBeforeTransport(t *testing.T) {
	for _, tc := range []struct {
		name   string
		modify func(*cmdctx.Ctx)
	}{
		{"dry-run disabled", func(c *cmdctx.Ctx) { c.SetFlag("dry-run", false) }},
		{"unsupported format", func(c *cmdctx.Ctx) { c.FormatFlags.Format = "yaml" }},
		{"mixed resources", func(c *cmdctx.Ctx) { c.SetFlag("resources", "secret,pipeline") }},
		{"no selection", func(c *cmdctx.Ctx) { c.SetFlag("resources", "") }},
		{"full", func(c *cmdctx.Ctx) { c.SetFlag("resources", "all") }},
		{"identity", func(c *cmdctx.Ctx) { c.MigrateTo = c.MigrateFrom }},
		{"source omitted", func(c *cmdctx.Ctx) { c.MigrateFrom = "" }},
		{"destination omitted", func(c *cmdctx.Ctx) { c.MigrateTo = "" }},
		{"project override", func(c *cmdctx.Ctx) { c.SetFlag("project", "other") }},
		{"org level", func(c *cmdctx.Ctx) { c.Level = "org" }},
		{"positional", func(c *cmdctx.Ctx) { c.Id = "unexpected" }},
		{"malformed scope", func(c *cmdctx.Ctx) { c.MigrateFrom = "source/other" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := copyContext()
			tc.modify(c)
			// A nil context and invalid API URL would panic/fail if validation reached HTTP.
			if err := projectCopy(c); err == nil {
				t.Fatal("unsupported work accepted")
			}
		})
	}
	c := copyContext()
	c.SetFlag("apply", true)
	if _, err := projectCopyScope(c); err != nil {
		t.Fatal(err)
	}
	c = copyContext()
	c.SetFlag("dry-run", true)
	c.SetFlag("apply", true)
	if projectCopyShouldApply(c) {
		t.Fatal("dry-run must not apply")
	}
	if _, err := projectCopyScope(c); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(t.TempDir(), "report")
	os.WriteFile(existing, []byte("kept"), 0600)
	c = copyContext()
	c.SetFlag("apply", true)
	c.FormatFlags.OutFile = existing
	if err := projectCopy(c); err == nil || !strings.Contains(err.Error(), "cannot create report") {
		t.Fatalf("existing --out must fail before copy: %v", err)
	}
	if data, _ := os.ReadFile(existing); string(data) != "kept" {
		t.Fatal("existing --out changed")
	}
	c = copyContext()
	s, err := projectCopyScope(c)
	if err != nil || s.Source != "source" || s.Destination != "destination" {
		t.Fatal("profile project replaced explicit locations")
	}
}

func TestProjectCopyReportsAndProtectedFiles(t *testing.T) {
	c := copyContext()
	scope, _ := projectCopyScope(c)
	r := projectcopy.NewReport(scope)
	r.Secrets = []projectcopy.Item{{Number: 1, Type: "SecretText", Capability: projectcopy.ReferenceOnly, Reason: "external reference configuration only"}}
	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			var buf bytes.Buffer
			if err := renderProjectCopyReport(&buf, format, r); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(buf.String(), "PARTIAL") || !strings.Contains(buf.String(), "destination") || strings.Contains(buf.String(), "ignored_profile_project") {
				t.Fatal("incorrect report scope/coverage")
			}
			c.FormatFlags = cmdctx.FormatFlags{Format: format, OutFile: filepath.Join(t.TempDir(), "report")}
			if err := writeProjectCopyReport(c, r); err != nil {
				t.Fatal(err)
			}
			info, _ := os.Stat(c.FormatFlags.OutFile)
			if info.Mode().Perm() != 0600 {
				t.Fatal("report permissions")
			}
			before, _ := os.ReadFile(c.FormatFlags.OutFile)
			if err := writeProjectCopyReport(c, r); err == nil {
				t.Fatal("existing report overwritten")
			}
			after, _ := os.ReadFile(c.FormatFlags.OutFile)
			if !bytes.Equal(before, after) {
				t.Fatal("existing data changed")
			}
		})
	}
	dir := t.TempDir()
	protected := filepath.Join(dir, "profiles.yaml")
	os.WriteFile(protected, []byte("SYNTHETIC_PROTECTED_INPUT"), 0600)
	for _, kind := range []string{"direct", "symlink", "hardlink"} {
		t.Run(kind, func(t *testing.T) {
			out := protected
			if kind != "direct" {
				out = filepath.Join(dir, kind)
				var err error
				if kind == "symlink" {
					err = os.Symlink(protected, out)
				} else {
					err = os.Link(protected, out)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			c.FormatFlags = cmdctx.FormatFlags{OutFile: out}
			if err := writeProjectCopyReport(c, r); err == nil {
				t.Fatal("protected alias accepted")
			}
			data, _ := os.ReadFile(protected)
			if string(data) != "SYNTHETIC_PROTECTED_INPUT" {
				t.Fatal("input overwritten")
			}
		})
	}
}
