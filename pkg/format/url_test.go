// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package format

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/harness/cli/v3/pkg/cmdctx"
)

func urlEnv(fn func(any) string) map[string]any {
	if fn == nil {
		return map[string]any{}
	}
	return map[string]any{"url": fn}
}

// outFile returns a FormatFlags.OutFile path under a fresh temp dir, and a reader that
// returns the file's contents. FormatSingleOutput/FormatArrayOutput write to OutFile via
// OpenWriter when it's non-empty, so this captures output without touching os.Stdout.
func outFile(t *testing.T) (string, func() []byte) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "out")
	return path, func() []byte {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading captured output: %v", err)
		}
		return b
	}
}

func TestInjectURL(t *testing.T) {
	always := func(it any) string { return "https://example.com/thing" }
	empty := func(it any) string { return "" }

	tests := []struct {
		name string
		env  map[string]any
		item any
		want any
	}{
		{
			name: "adds url when fn present and item is a map",
			env:  urlEnv(always),
			item: map[string]any{"id": "abc"},
			want: map[string]any{"id": "abc", "url": "https://example.com/thing"},
		},
		{
			name: "no-op when url fn not registered",
			env:  urlEnv(nil),
			item: map[string]any{"id": "abc"},
			want: map[string]any{"id": "abc"},
		},
		{
			name: "no-op when resolved url is empty",
			env:  urlEnv(empty),
			item: map[string]any{"id": "abc"},
			want: map[string]any{"id": "abc"},
		},
		{
			name: "no-op when item already has a url key",
			env:  urlEnv(always),
			item: map[string]any{"id": "abc", "url": "https://api.example.com/native"},
			want: map[string]any{"id": "abc", "url": "https://api.example.com/native"},
		},
		{
			name: "no-op when item is not a map",
			env:  urlEnv(always),
			item: []any{"a", "b"},
			want: []any{"a", "b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := injectURL(tt.env, tt.item)
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(tt.want)
			if string(gotJSON) != string(wantJSON) {
				t.Errorf("injectURL() = %s, want %s", gotJSON, wantJSON)
			}
		})
	}
}

func TestFormatSingleOutput_JSONIncludesURL(t *testing.T) {
	env := urlEnv(func(it any) string { return "https://example.com/pipelines/p1" })
	data := map[string]any{"identifier": "p1"}

	path, read := outFile(t)
	flags := cmdctx.FormatFlags{Format: "json", OutFile: path}

	if err := FormatSingleOutput(flags, false, data, "it", "", nil, nil, env); err != nil {
		t.Fatalf("FormatSingleOutput: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal(read(), &out); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if out["url"] != "https://example.com/pipelines/p1" {
		t.Errorf("expected url field in JSON output, got %v", out)
	}
}

func TestFormatSingleOutput_RawSkipsURL(t *testing.T) {
	env := urlEnv(func(it any) string { return "https://example.com/pipelines/p1" })
	data := map[string]any{"identifier": "p1"}

	path, read := outFile(t)
	flags := cmdctx.FormatFlags{Format: "json", Raw: true, OutFile: path}

	if err := FormatSingleOutput(flags, false, data, "it", "", nil, nil, env); err != nil {
		t.Fatalf("FormatSingleOutput: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal(read(), &out); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if _, exists := out["url"]; exists {
		t.Errorf("expected no url field with --raw, got %v", out)
	}
}

func TestFormatArrayOutput_JSONIncludesURLPerItem(t *testing.T) {
	env := urlEnv(func(it any) string {
		m, _ := it.(map[string]any)
		return "https://example.com/executions/" + m["id"].(string)
	})
	data := []any{
		map[string]any{"id": "e1"},
		map[string]any{"id": "e2"},
	}

	path, read := outFile(t)
	flags := cmdctx.FormatFlags{Format: "json", OutFile: path}

	if err := FormatArrayOutput(flags, false, data, "it", nil, nil, env, nil); err != nil {
		t.Fatalf("FormatArrayOutput: %v", err)
	}

	var out []map[string]any
	if err := json.Unmarshal(read(), &out); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 items, got %d", len(out))
	}
	if out[0]["url"] != "https://example.com/executions/e1" {
		t.Errorf("item 0: expected url, got %v", out[0])
	}
	if out[1]["url"] != "https://example.com/executions/e2" {
		t.Errorf("item 1: expected url, got %v", out[1])
	}
}

func TestFormatArrayOutput_JSONLIncludesURLPerItem(t *testing.T) {
	env := urlEnv(func(it any) string {
		m, _ := it.(map[string]any)
		return "https://example.com/executions/" + m["id"].(string)
	})
	data := []any{
		map[string]any{"id": "e1"},
	}

	path, read := outFile(t)
	flags := cmdctx.FormatFlags{Format: "jsonl", OutFile: path}

	if err := FormatArrayOutput(flags, false, data, "it", nil, nil, env, nil); err != nil {
		t.Fatalf("FormatArrayOutput: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal(read(), &out); err != nil {
		t.Fatalf("unmarshal jsonl line: %v", err)
	}
	if out["url"] != "https://example.com/executions/e1" {
		t.Errorf("expected url in jsonl output, got %v", out)
	}
}
