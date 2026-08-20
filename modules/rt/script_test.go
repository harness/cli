// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package rt

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harness/cli/pkg/cmdctx"
)

// A DataAccessor over a flat map: the formatter reads two paths and nothing nested.
type fakeData map[string]any

func (f fakeData) GetString(path string) string { s, _ := f[path].(string); return s }
func (f fakeData) GetBool(path string) bool     { b, _ := f[path].(bool); return b }
func (f fakeData) GetInt64(path string) int64   { i, _ := f[path].(int64); return i }
func (f fakeData) GetTs(path string) string     { return f.GetString(path) }
func (f fakeData) GetData() any                 { return map[string]any(f) }
func (f fakeData) GetSlice(path string) []any   { s, _ := f[path].([]any); return s }

func TestFormatScript(t *testing.T) {
	const script = "openapi: 3.0.0\nplan: checkout\n"
	var buf bytes.Buffer
	err := formatScript(&buf, fakeData{"it.scriptContent": base64.StdEncoding.EncodeToString([]byte(script))})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if buf.String() != script {
		t.Fatalf("got %q, want the script back unchanged", buf.String())
	}
}

func TestFormatScriptEmpty(t *testing.T) {
	err := formatScript(&bytes.Buffer{}, fakeData{})
	if err == nil || !strings.Contains(err.Error(), "container image") {
		t.Fatalf("expected the container-image explanation, got %v", err)
	}
}

func TestFormatScriptBadBase64(t *testing.T) {
	err := formatScript(&bytes.Buffer{}, fakeData{"it.scriptContent": "not base64!!"})
	if err == nil || !strings.Contains(err.Error(), "decoding") {
		t.Fatalf("expected a decode error, got %v", err)
	}
}

func TestFormatScriptBundleToFile(t *testing.T) {
	const archive = "PK\x03\x04zip bytes"
	var buf bytes.Buffer
	d := fakeData{
		"it.scriptContent":  base64.StdEncoding.EncodeToString([]byte(archive)),
		"it.isBundle":       true,
		"it.bundleMainFile": "checkout.jmx",
	}
	if err := formatScript(&buf, d); err != nil {
		t.Fatalf("a bundle written to a file is fine, got %v", err)
	}
	if buf.String() != archive {
		t.Fatalf("got %q, want the archive intact", buf.String())
	}
}

func TestBundleKind(t *testing.T) {
	got := bundleKind(fakeData{"it.bundleMainFile": "checkout.jmx"})
	if !strings.Contains(got, "checkout.jmx") {
		t.Errorf("got %q, want the main plan named", got)
	}
	if got := bundleKind(fakeData{}); got != "a zip workspace" {
		t.Errorf("got %q, want the bare description when no main plan is recorded", got)
	}
}

func TestEncodeScriptBody(t *testing.T) {
	const script = "plan: checkout\n"
	path := filepath.Join(t.TempDir(), "checkout.jmx")
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}

	body, err := encodeScriptBody(&cmdctx.Ctx{FlagValues: map[string]any{"file": path}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m, ok := body.(map[string]any)
	if !ok {
		t.Fatalf("body is %T, want a map", body)
	}
	if m["scriptContent"] != base64.StdEncoding.EncodeToString([]byte(script)) {
		t.Errorf("scriptContent = %v, want the encoded file", m["scriptContent"])
	}
	if _, present := m["description"]; present {
		t.Error("an absent --description should be left out, not sent empty")
	}
}

func TestEncodeScriptBodyWithDescription(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkout.jmx")
	if err := os.WriteFile(path, []byte("plan: checkout\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	body, err := encodeScriptBody(&cmdctx.Ctx{FlagValues: map[string]any{
		"file": path, "description": "peak traffic",
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m, _ := body.(map[string]any); m["description"] != "peak traffic" {
		t.Errorf("description = %v, want it carried through", m["description"])
	}
}

func TestEncodeScriptBodyEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.jmx")
	if err := os.WriteFile(path, []byte("   \n\t\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := encodeScriptBody(&cmdctx.Ctx{FlagValues: map[string]any{"file": path}})
	if err == nil || !strings.Contains(err.Error(), "nothing to upload") {
		t.Fatalf("expected a refusal to upload an empty script, got %v", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("expected the path in %q", err)
	}
}

func TestEncodeScriptBodyMissingFlag(t *testing.T) {
	if _, err := encodeScriptBody(&cmdctx.Ctx{FlagValues: map[string]any{}}); err == nil {
		t.Fatal("expected an error when -f is absent")
	}
}

func TestResolveScriptRevisionByNumber(t *testing.T) {
	ctx, calls := apiCtx(t, map[string]any{
		api("/load-tests/checkout/script/revisions"): itemsPage(
			map[string]any{"revisionNumber": float64(1), "identity": "rev-one"},
			map[string]any{"revisionNumber": float64(2), "identity": "rev-two"},
		),
	})
	ctx.Id = "checkout"

	got, err := resolveScriptRevision(ctx, "2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "rev-two" {
		t.Errorf("got %q, want the identity of revision 2", got)
	}
	if _, ok := findCall(calls, "GET", api("/load-tests/checkout/script/revisions")); !ok {
		t.Error("the revisions of the load test were never read")
	}
}

func TestResolveScriptRevisionTrimsTheNumber(t *testing.T) {
	ctx, _ := apiCtx(t, map[string]any{
		api("/load-tests/checkout/script/revisions"): itemsPage(
			map[string]any{"revisionNumber": float64(2), "identity": "rev-two"},
		),
	})
	ctx.Id = "checkout"

	if got, err := resolveScriptRevision(ctx, " 2 "); err != nil || got != "rev-two" {
		t.Errorf("got %q, %v; want rev-two", got, err)
	}
}

func TestResolveScriptRevisionPassesIdentifiersThrough(t *testing.T) {
	ctx, calls := apiCtx(t, nil)
	ctx.Id = "checkout"

	for _, raw := range []string{"rev-two", "abc123", "2.0", "v2"} {
		got, err := resolveScriptRevision(ctx, raw)
		if err != nil || got != raw {
			t.Errorf("resolveScriptRevision(%q) = %q, %v; want it passed through", raw, got, err)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("made %d requests, want an identifier resolved without asking the API", len(*calls))
	}
}

func TestResolveScriptRevisionNeedsALoadTest(t *testing.T) {
	ctx, _ := apiCtx(t, nil)

	_, err := resolveScriptRevision(ctx, "2")
	if err == nil || !strings.Contains(err.Error(), "load test id") {
		t.Fatalf("expected the missing load test explained, got %v", err)
	}
}

func TestResolveScriptRevisionUnknownNumber(t *testing.T) {
	ctx, _ := apiCtx(t, map[string]any{
		api("/load-tests/checkout/script/revisions"): itemsPage(
			map[string]any{"revisionNumber": float64(1), "identity": "rev-one"},
		),
	})
	ctx.Id = "checkout"

	_, err := resolveScriptRevision(ctx, "7")
	if err == nil {
		t.Fatal("expected an unknown revision number to be refused")
	}
	if !strings.Contains(err.Error(), "list loadtest_script:revisions") {
		t.Errorf("error %q should point at the command that lists them", err)
	}
}

func TestResolveScriptRevisionReportsAnUnreadableList(t *testing.T) {
	ctx, _ := apiCtx(t, nil)
	ctx.Id = "checkout"

	_, err := resolveScriptRevision(ctx, "2")
	if err == nil || !strings.Contains(err.Error(), "checkout") {
		t.Fatalf("expected the load test named in the error, got %v", err)
	}
}
