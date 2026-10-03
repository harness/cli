// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package fme

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/harness/cli/v3/pkg/cmdctx"
)

func TestParseKeys(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"one per line", "a\nb\nc\n", []string{"a", "b", "c"}},
		{"single row", "a,b,c,d\n", []string{"a", "b", "c", "d"}},
		{"grid", "a,b\nc,d\n", []string{"a", "b", "c", "d"}},
		{"crlf", "a\r\nb\r\n", []string{"a", "b"}},
		{"bom", "\xef\xbb\xbfa\nb\n", []string{"a", "b"}},
		{"trim blanks dedupe", " a , b\n\n\na,,c\n", []string{"a", "b", "c"}},
		{"quoted comma", "\"a,b\",c\n", []string{"a,b", "c"}},
		{"empty", "\n\n", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseKeys(strings.NewReader(tt.in))
			if err != nil {
				t.Fatalf("parseKeys: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveKeysFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.csv")
	if err := os.WriteFile(path, []byte("a,b\nc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := resolveKeysFile(nil, path)
	if err != nil {
		t.Fatalf("resolveKeysFile: %v", err)
	}
	if res.Value != "a\nb\nc" {
		t.Fatalf("Value = %q, want %q", res.Value, "a\nb\nc")
	}
}

func TestResolveKeysFile_Errors(t *testing.T) {
	if _, err := resolveKeysFile(nil, filepath.Join(t.TempDir(), "missing.csv")); err == nil {
		t.Fatal("expected error for missing file")
	}
	empty := filepath.Join(t.TempDir(), "empty.csv")
	if err := os.WriteFile(empty, []byte(" \n,\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveKeysFile(nil, empty); err == nil || !strings.Contains(err.Error(), "no keys found") {
		t.Fatalf("err = %v, want no keys found", err)
	}
}

func TestValidateSegmentKeys(t *testing.T) {
	many := make([]any, maxSegmentKeysPerCall+1)
	for i := range many {
		many[i] = fmt.Sprint(i)
	}
	tests := []struct {
		name    string
		req     cmdctx.EndpointRequest
		wantErr string
	}{
		{"ok", cmdctx.EndpointRequest{Body: map[string]any{"keys": []any{"a"}}}, ""},
		{"empty", cmdctx.EndpointRequest{Body: map[string]any{"keys": []any{}}}, "no keys given"},
		{"empty with replace", cmdctx.EndpointRequest{QueryParams: map[string]string{"replace": "true"}, Body: map[string]any{"keys": []any{}}}, ""},
		{"too many", cmdctx.EndpointRequest{Body: map[string]any{"keys": many}}, "exceeds the limit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSegmentKeys(nil, tt.req)
			if tt.wantErr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
		})
	}
}
