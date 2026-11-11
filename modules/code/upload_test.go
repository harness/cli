// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package code

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckUploadable(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cases := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"png", write("a.png", []byte("x")), false},
		{"upper-case ext", write("b.MP4", []byte("x")), false},
		{"unsupported ext", write("c.exe", []byte("x")), true},
		{"no ext", write("d", []byte("x")), true},
		{"empty", write("e.png", nil), true},
		{"directory", dir, true},
		{"missing", filepath.Join(dir, "nope.png"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := checkUploadable(tc.path)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestUploadSnippet(t *testing.T) {
	cases := []struct{ name, want string }{
		{"shot.png", "![shot.png](u)"},
		{"clip.MOV", "[clip.MOV](u)"},
		{"a[1].png", `![a\[1\].png](u)`},
	}
	for _, tc := range cases {
		if got := uploadSnippet(tc.name, "u"); got != tc.want {
			t.Errorf("uploadSnippet(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}
