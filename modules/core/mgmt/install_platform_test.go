// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package mgmt

import (
	"archive/zip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/harness/cli/v3/pkg/release"
)

func TestArchiveExtensionForPlatform(t *testing.T) {
	tests := []struct {
		platform string
		want     string
	}{
		{"linux_amd64", ".tar.gz"},
		{"linux_arm64", ".tar.gz"},
		{"darwin_amd64", ".tar.gz"},
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

func TestArchiveAssetURL(t *testing.T) {
	rel := &release.Release{
		TagName: "v1.2.3",
		Assets: []release.Asset{
			{Name: "harness-core_1.2.3_windows_amd64.zip", BrowserDownloadURL: "https://example.com/win.zip"},
			{Name: "harness-core_1.2.3_linux_amd64.tar.gz", BrowserDownloadURL: "https://example.com/nix.tar.gz"},
			{Name: "harness_1.2.3_checksums.txt", BrowserDownloadURL: "https://example.com/checksums.txt"},
		},
	}

	winURL, err := archiveAssetURL(rel, "harness-core", "v1.2.3", "windows_amd64")
	if err != nil {
		t.Fatalf("archiveAssetURL: %v", err)
	}
	if winURL != "https://example.com/win.zip" {
		t.Errorf("windows archive URL = %q", winURL)
	}

	nixURL, err := archiveAssetURL(rel, "harness-core", "v1.2.3", "linux_amd64")
	if err != nil {
		t.Fatalf("archiveAssetURL: %v", err)
	}
	if nixURL != "https://example.com/nix.tar.gz" {
		t.Errorf("linux archive URL = %q", nixURL)
	}

	checksumURL, err := checksumAssetURL(rel)
	if err != nil {
		t.Fatalf("checksumAssetURL: %v", err)
	}
	if checksumURL != "https://example.com/checksums.txt" {
		t.Errorf("checksum URL = %q", checksumURL)
	}
}

func TestArchiveAssetURLMissing(t *testing.T) {
	rel := &release.Release{TagName: "v1.2.3"}
	if _, err := archiveAssetURL(rel, "harness-core", "v1.2.3", "linux_amd64"); err == nil {
		t.Fatal("expected an error when no matching asset exists")
	}
	if _, err := checksumAssetURL(rel); err == nil {
		t.Fatal("expected an error when no checksums asset exists")
	}
}

func TestInstalledBinaryName(t *testing.T) {
	got := installedBinaryName("harness")
	want := "harness"
	if runtime.GOOS == "windows" {
		want = "harness.exe"
	}
	if got != want {
		t.Errorf("installedBinaryName(\"harness\") = %q, want %q", got, want)
	}
	// An explicit .exe is never doubled.
	if got := installedBinaryName("harness.exe"); got != "harness.exe" {
		t.Errorf("installedBinaryName(\"harness.exe\") = %q, want %q", got, "harness.exe")
	}
}

func TestDefaultInstallDir(t *testing.T) {
	dir, err := defaultInstallDir()
	if err != nil {
		t.Fatalf("defaultInstallDir: %v", err)
	}
	if dir == "" {
		t.Fatal("defaultInstallDir returned an empty path")
	}
	if runtime.GOOS == "windows" {
		if !strings.Contains(dir, filepath.Join("Programs", "harness")) {
			t.Errorf("defaultInstallDir on windows = %q, want a Programs\\harness path", dir)
		}
		return
	}
	if dir != "~/.local/bin" {
		t.Errorf("defaultInstallDir = %q, want %q", dir, "~/.local/bin")
	}
}

func TestExtractBinaryFromZip(t *testing.T) {
	tmp := t.TempDir()
	archivePath := filepath.Join(tmp, "bundle.zip")
	writeTestZip(t, archivePath, map[string]string{
		"LICENSE":         "license text",
		"harness.exe":     "fake-core",
		"harness-har.exe": "fake-plugin",
	})

	dest := filepath.Join(tmp, "harness.exe")
	if err := extractBinaryFromArchive(archivePath, "harness.exe", dest, ".zip"); err != nil {
		t.Fatalf("extractBinaryFromArchive: %v", err)
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "fake-core" {
		t.Fatalf("extracted content = %q, want %q", string(data), "fake-core")
	}
}

func TestExtractBinaryFromZipMissing(t *testing.T) {
	tmp := t.TempDir()
	archivePath := filepath.Join(tmp, "bundle.zip")
	writeTestZip(t, archivePath, map[string]string{"LICENSE": "license text"})

	err := extractBinaryFromZip(archivePath, "harness.exe", filepath.Join(tmp, "out.exe"))
	if err == nil {
		t.Fatal("expected an error when the binary is absent from the archive")
	}
}

func TestDetectPlatform(t *testing.T) {
	platform, err := detectPlatform()
	if err != nil {
		t.Fatalf("detectPlatform: %v", err)
	}
	if !strings.HasPrefix(platform, runtime.GOOS+"_") {
		t.Fatalf("detectPlatform = %q, want a %s_* platform", platform, runtime.GOOS)
	}
}

func writeTestZip(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := zip.NewWriter(f)
	for name, content := range entries {
		e, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}
