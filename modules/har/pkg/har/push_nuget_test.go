// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"archive/zip"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

type zipEntry struct{ name, body string }

func writeNupkg(t *testing.T, entries ...zipEntry) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pkg.nupkg")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, e := range entries {
		w, err := zw.Create(e.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func nuspec(id, version string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://schemas.microsoft.com/packaging/2013/05/nuspec.xsd">
  <metadata><id>%s</id><version>%s</version></metadata>
</package>`, id, version)
}

func TestNormalizeNugetVersion(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "1.0", want: "1.0.0"},
		{in: "1.2.3.0", want: "1.2.3"},
		{in: "1.2.3.4", want: "1.2.3.4"},
		{in: "v2.0.1", want: "2.0.1"},
		{in: "01.002.3", want: "1.2.3"},
		{in: "1.0.0-Beta.1", want: "1.0.0-beta.1"},
		{in: "1.0.0+build.5", want: "1.0.0"},
		{in: "1.0.0-rc1+sha.abc", want: "1.0.0-rc1"},
		{in: "1.2.3.4.5", wantErr: true},
		{in: "not-a-version", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := normalizeNugetVersion(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReadNugetIdentity(t *testing.T) {
	tests := []struct {
		name        string
		entries     []zipEntry
		wantID      string
		wantVersion string
		wantErr     bool
	}{
		{
			name:        "root nuspec, id lowercased and version normalized",
			entries:     []zipEntry{{"My.Pkg.nuspec", nuspec("My.Pkg", "2.1-Preview")}, {"lib/a.dll", "x"}},
			wantID:      "my.pkg",
			wantVersion: "2.1.0-preview",
		},
		{
			name:        "nested nuspec counts",
			entries:     []zipEntry{{"sub/dir/My.Pkg.nuspec", nuspec("My.Pkg", "1.0.0")}},
			wantID:      "my.pkg",
			wantVersion: "1.0.0",
		},
		{
			name: "last nuspec wins",
			entries: []zipEntry{
				{"first.nuspec", nuspec("First", "1.0.0")},
				{"second.nuspec", nuspec("Second", "2.0.0")},
			},
			wantID:      "second",
			wantVersion: "2.0.0",
		},
		{
			name:    "uppercase extension is not a nuspec",
			entries: []zipEntry{{"My.Pkg.NUSPEC", nuspec("My.Pkg", "1.0.0")}},
			wantErr: true,
		},
		{
			name: "any malformed nuspec fails",
			entries: []zipEntry{
				{"good.nuspec", nuspec("Good", "1.0.0")},
				{"bad.nuspec", "<package><metadata>"},
			},
			wantErr: true,
		},
		{
			name:    "missing version",
			entries: []zipEntry{{"My.Pkg.nuspec", nuspec("My.Pkg", "")}},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, version, err := readNugetIdentity(writeNupkg(t, tt.entries...))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got (%q, %q)", id, version)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if id != tt.wantID || version != tt.wantVersion {
				t.Errorf("got (%q, %q), want (%q, %q)", id, version, tt.wantID, tt.wantVersion)
			}
		})
	}
}

func TestReadNugetIdentity_NotAZip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.nupkg")
	if err := os.WriteFile(path, []byte("not a zip"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readNugetIdentity(path); err == nil {
		t.Fatal("expected error for a non-zip file")
	}
}
