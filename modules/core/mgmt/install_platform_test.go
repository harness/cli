// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package mgmt

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestArchiveExtensionForPlatform(t *testing.T) {
	tests := []struct {
		platform string
		want     string
	}{
		{"linux_amd64", ".tar.gz"},
		{"darwin_arm64", ".tar.gz"},
		{"windows_amd64", ".zip"},
		{"windows_arm64", ".zip"},
	}
	for _, tt := range tests {
		if got := archiveExtensionForPlatform(tt.platform); got != tt.want {
			t.Errorf("archiveExtensionForPlatform(%q) = %q, want %q", tt.platform, got, tt.want)
		}
	}
}

func TestExtractBinaryFromZip(t *testing.T) {
	tmp := t.TempDir()
	archivePath := filepath.Join(tmp, "bundle.zip")
	dest := filepath.Join(tmp, "harness.exe")

	zf, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(zf)
	f, err := w.Create("harness.exe")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("fake-binary")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zf.Close(); err != nil {
		t.Fatal(err)
	}

	if err := extractBinaryFromZip(archivePath, "harness.exe", dest); err != nil {
		t.Fatalf("extractBinaryFromZip: %v", err)
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "fake-binary" {
		t.Fatalf("extracted content = %q, want %q", string(data), "fake-binary")
	}
}

func TestDetectPlatform(t *testing.T) {
	platform, err := detectPlatform()
	if err != nil {
		t.Fatalf("detectPlatform: %v", err)
	}
	if platform == "" {
		t.Fatal("detectPlatform returned empty platform")
	}
}
