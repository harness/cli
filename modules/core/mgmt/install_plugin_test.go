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

	"github.com/harness/cli/pkg/release"
)

func TestLooksLikePath(t *testing.T) {
	tests := []struct {
		ref  string
		want bool
	}{
		{"/abs/path/foo.tar.gz", true},
		{"~/foo.tar.gz", true},
		{"./foo.tar.gz", true},
		{"./bin/harness-har", true},
		{"foo.tar.gz", false},          // bareword, no marker — not a path
		{"dist/foo.tar.gz", false},     // relative with a slash but no "./" — not a path
		{"harness-har", false},         // bareword, even if a same-named file exists in cwd
		{"someorg/some-plugin", false}, // owner/repo shape
		{"~foo/bar", false},            // "~" not followed by "/" doesn't count
	}
	for _, tt := range tests {
		if got := looksLikePath(tt.ref); got != tt.want {
			t.Errorf("looksLikePath(%q) = %v, want %v", tt.ref, got, tt.want)
		}
	}
}

func TestSplitGitHubRef(t *testing.T) {
	tests := []struct {
		ref                             string
		wantOwner, wantRepo, wantPrefix string
		wantOK                          bool
	}{
		{"someorg/some-plugin", "someorg", "some-plugin", "", true},
		{"someorg/some-plugin/myplugin", "someorg", "some-plugin", "myplugin", true},
		{"a/b/c/d", "", "", "", false},
		{"onlyoneword", "", "", "", false},
		{"owner/", "", "", "", false},
		{"/repo", "", "", "", false},
		{"owner/repo/", "", "", "", false},
	}
	for _, tt := range tests {
		owner, repoName, prefix, ok := splitGitHubRef(tt.ref)
		if ok != tt.wantOK || owner != tt.wantOwner || repoName != tt.wantRepo || prefix != tt.wantPrefix {
			t.Errorf("splitGitHubRef(%q) = (%q, %q, %q, %v), want (%q, %q, %q, %v)",
				tt.ref, owner, repoName, prefix, ok, tt.wantOwner, tt.wantRepo, tt.wantPrefix, tt.wantOK)
		}
	}
}

func TestParsePluginRef(t *testing.T) {
	tests := []struct {
		name string
		ref  string
		want pluginRef
	}{
		{"url", "https://example.com/foo.tar.gz", pluginRef{URL: "https://example.com/foo.tar.gz"}},
		{"http url", "http://example.com/foo.tar.gz", pluginRef{URL: "http://example.com/foo.tar.gz"}},
		{"absolute path", "/abs/path/foo.tar.gz", pluginRef{LocalPath: "/abs/path/foo.tar.gz"}},
		{"home path", "~/foo.tar.gz", pluginRef{LocalPath: "~/foo.tar.gz"}},
		{"dot path", "./foo.tar.gz", pluginRef{LocalPath: "./foo.tar.gz"}},
		{
			"owner/repo", "someorg/some-plugin",
			pluginRef{GithubRef: &GithubPluginRef{GithubRepo: "someorg/some-plugin"}},
		},
		{
			"owner/repo/prefix", "someorg/some-plugin/myplugin",
			pluginRef{GithubRef: &GithubPluginRef{GithubRepo: "someorg/some-plugin", TagPrefix: "myplugin"}},
		},
		{
			"registry name", "har",
			pluginRef{PluginName: "har"},
		},
		{
			"unregistered but valid name", "notaplugin",
			pluginRef{PluginName: "notaplugin"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parsePluginRef(tt.ref)
			if err != nil {
				t.Fatalf("parsePluginRef(%q): %v", tt.ref, err)
			}
			if got.URL != tt.want.URL || got.LocalPath != tt.want.LocalPath || got.PluginName != tt.want.PluginName {
				t.Fatalf("parsePluginRef(%q) = %+v, want %+v", tt.ref, got, tt.want)
			}
			switch {
			case tt.want.GithubRef == nil && got.GithubRef != nil:
				t.Fatalf("parsePluginRef(%q).GithubRef = %+v, want nil", tt.ref, got.GithubRef)
			case tt.want.GithubRef != nil && got.GithubRef == nil:
				t.Fatalf("parsePluginRef(%q).GithubRef = nil, want %+v", tt.ref, tt.want.GithubRef)
			case tt.want.GithubRef != nil && *got.GithubRef != *tt.want.GithubRef:
				t.Fatalf("parsePluginRef(%q).GithubRef = %+v, want %+v", tt.ref, got.GithubRef, tt.want.GithubRef)
			}
		})
	}

	errTests := []struct {
		name, ref, wantSubstr string
	}{
		{"bad archive ref", "dist/foo.tar.gz", "not a path harness recognizes"},
		{"bad github ref shape", "a/b/c/d", "not a valid owner/repo"},
		{"invalid plugin name syntax", "Not_Valid", "not a URL, an existing file"},
	}
	for _, tt := range errTests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parsePluginRef(tt.ref)
			if err == nil {
				t.Fatalf("parsePluginRef(%q): expected an error", tt.ref)
			}
			if !strings.Contains(err.Error(), tt.wantSubstr) {
				t.Fatalf("parsePluginRef(%q) error = %v, want it to contain %q", tt.ref, err, tt.wantSubstr)
			}
		})
	}
}

func TestInstallRegistryPluginUnknownName(t *testing.T) {
	err := installRegistryPlugin("notaplugin", "", "", false, false, false)
	if err == nil {
		t.Fatal("installRegistryPlugin(\"notaplugin\"): expected an error")
	}
	if !strings.Contains(err.Error(), "unknown plugin") {
		t.Fatalf("installRegistryPlugin(\"notaplugin\") error = %v, want it to contain %q", err, "unknown plugin")
	}
}

func TestDiscoverPluginAsset(t *testing.T) {
	rel := &release.Release{
		TagName: "v1.2.3",
		Assets: []release.Asset{
			{Name: "harness-core_1.2.3_darwin_amd64.tar.gz"},
			{Name: "harness-plugin-har_1.2.3_darwin_amd64.tar.gz"},
			{Name: "harness-plugin-har_1.2.3_linux_amd64.tar.gz"},
			{Name: "harness_1.2.3_checksums.txt"},
		},
	}
	asset, name, err := discoverPluginAsset(rel, "v1.2.3", "darwin_amd64")
	if err != nil {
		t.Fatalf("discoverPluginAsset: %v", err)
	}
	if name != "har" {
		t.Errorf("name = %q, want %q", name, "har")
	}
	if asset.Name != "harness-plugin-har_1.2.3_darwin_amd64.tar.gz" {
		t.Errorf("asset = %q, want the darwin_amd64 asset", asset.Name)
	}

	if _, _, err := discoverPluginAsset(rel, "v1.2.3", "windows_amd64"); err == nil {
		t.Fatal("expected an error for a platform with no plugin asset")
	}

	multi := &release.Release{
		TagName: "v1.2.3",
		Assets: []release.Asset{
			{Name: "harness-plugin-har_1.2.3_darwin_amd64.tar.gz"},
			{Name: "harness-plugin-foo_1.2.3_darwin_amd64.tar.gz"},
		},
	}
	if _, _, err := discoverPluginAsset(multi, "v1.2.3", "darwin_amd64"); err == nil {
		t.Fatal("expected an error when more than one plugin asset matches")
	} else if !strings.Contains(err.Error(), "--plugin-name") {
		t.Fatalf("error = %v, want it to mention --plugin-name", err)
	}
}

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
