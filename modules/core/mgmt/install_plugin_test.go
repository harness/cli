// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package mgmt

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsArchive(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"plugin.tar.gz", true},
		{"plugin.tgz", true},
		{"harness-bundle_1.0.0_windows_amd64.zip", true},
		{"PLUGIN.ZIP", true},
		{"harness-har", false},
		{"harness-har.exe", false},
	}
	for _, tt := range tests {
		if got := isArchive(tt.path); got != tt.want {
			t.Errorf("isArchive(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestExtractPluginBinaryFromZip(t *testing.T) {
	tmp := t.TempDir()
	archivePath := filepath.Join(tmp, "bundle.zip")
	// The core `harness` binary and a license co-ship in the real bundle; only
	// the harness-<name> entry may match.
	writeTestZip(t, archivePath, map[string]string{
		"LICENSE":         "license text",
		"harness.exe":     "fake-core",
		"harness-har.exe": "fake-plugin",
	})

	destDir := filepath.Join(tmp, "extract")
	if err := os.MkdirAll(destDir, 0700); err != nil {
		t.Fatal(err)
	}
	got, err := extractPluginBinary(archivePath, destDir)
	if err != nil {
		t.Fatalf("extractPluginBinary: %v", err)
	}
	if filepath.Base(got) != "harness-har.exe" {
		t.Fatalf("extracted %q, want harness-har.exe", filepath.Base(got))
	}
	data, err := os.ReadFile(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "fake-plugin" {
		t.Fatalf("extracted content = %q, want %q", string(data), "fake-plugin")
	}
	// Nothing else may be written to the staging dir.
	entries, err := os.ReadDir(destDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("staging dir holds %d entries, want only the plugin binary", len(entries))
	}
}

func TestExtractPluginBinaryZipNoPlugin(t *testing.T) {
	tmp := t.TempDir()
	archivePath := filepath.Join(tmp, "bundle.zip")
	writeTestZip(t, archivePath, map[string]string{
		"LICENSE":     "license text",
		"harness.exe": "fake-core",
	})

	_, err := extractPluginBinary(archivePath, tmp)
	if err == nil {
		t.Fatal("expected an error for an archive with no harness-<name> entry")
	}
	if !strings.Contains(err.Error(), "not a harness plugin archive") {
		t.Fatalf("error = %v, want a not-a-plugin-archive diagnosis", err)
	}
}

func TestExtractPluginBinaryZipMultiplePlugins(t *testing.T) {
	tmp := t.TempDir()
	archivePath := filepath.Join(tmp, "bundle.zip")
	writeTestZip(t, archivePath, map[string]string{
		"harness-har.exe": "one",
		"harness-foo.exe": "two",
	})

	_, err := extractPluginBinary(archivePath, tmp)
	if err == nil {
		t.Fatal("expected an error for an archive holding two plugin binaries")
	}
	if !strings.Contains(err.Error(), "install one plugin at a time") {
		t.Fatalf("error = %v, want a one-plugin-at-a-time diagnosis", err)
	}
}

func TestExtractPluginBinaryFromTar(t *testing.T) {
	tmp := t.TempDir()
	archivePath := filepath.Join(tmp, "bundle.tar.gz")
	writeTestTarGz(t, archivePath, map[string]string{
		"LICENSE":     "license text",
		"harness":     "fake-core",
		"harness-har": "fake-plugin",
	})

	destDir := filepath.Join(tmp, "extract")
	if err := os.MkdirAll(destDir, 0700); err != nil {
		t.Fatal(err)
	}
	got, err := extractPluginBinary(archivePath, destDir)
	if err != nil {
		t.Fatalf("extractPluginBinary: %v", err)
	}
	if filepath.Base(got) != "harness-har" {
		t.Fatalf("extracted %q, want harness-har", filepath.Base(got))
	}
}

// TestExtractPluginBinaryFlattensPaths covers the hostile-archive case: an entry
// naming a parent directory must not escape destDir.
func TestExtractPluginBinaryFlattensPaths(t *testing.T) {
	tmp := t.TempDir()
	archivePath := filepath.Join(tmp, "bundle.zip")
	writeTestZip(t, archivePath, map[string]string{
		"../../harness-har.exe": "escaped",
	})

	destDir := filepath.Join(tmp, "extract")
	if err := os.MkdirAll(destDir, 0700); err != nil {
		t.Fatal(err)
	}
	got, err := extractPluginBinary(archivePath, destDir)
	if err != nil {
		t.Fatalf("extractPluginBinary: %v", err)
	}
	if filepath.Dir(got) != destDir {
		t.Fatalf("extracted to %q, want it contained in %q", got, destDir)
	}
}

func writeTestTarGz(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gzw := gzip.NewWriter(f)
	tw := tar.NewWriter(gzw)
	for name, content := range entries {
		hdr := &tar.Header{
			Name:     name,
			Typeflag: tar.TypeReg,
			Mode:     0755,
			Size:     int64(len(content)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatal(err)
	}
}
