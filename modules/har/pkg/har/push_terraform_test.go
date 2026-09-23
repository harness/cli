// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseTerraformProviderFilename(t *testing.T) {
	valid := []struct {
		filename string
		typeName string
		version  string
		osName   string
		arch     string
	}{
		{"terraform-provider-aws_5.31.0_linux_amd64.zip", "aws", "5.31.0", "linux", "amd64"},
		{"terraform-provider-google-beta_4.0.0_darwin_arm64.zip", "google-beta", "4.0.0", "darwin", "arm64"},
		{"terraform-provider-aws_1.2.3-rc1_linux_386.zip", "aws", "1.2.3-rc1", "linux", "386"},
		{"terraform-provider-aws_1.2.3+build5_windows_amd64.zip", "aws", "1.2.3+build5", "windows", "amd64"},
	}
	for _, tc := range valid {
		typeName, version, osName, arch, err := parseTerraformProviderFilename(tc.filename)
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tc.filename, err)
			continue
		}
		if typeName != tc.typeName || version != tc.version || osName != tc.osName || arch != tc.arch {
			t.Errorf("%s: got (%s, %s, %s, %s), want (%s, %s, %s, %s)",
				tc.filename, typeName, version, osName, arch, tc.typeName, tc.version, tc.osName, tc.arch)
		}
	}

	invalid := []string{
		"terraform-provider-aws_5.31.0_linux_amd64.tar.gz", // wrong extension
		"terraform-provider-aws_5.31_linux_amd64.zip",      // version not x.y.z
		"terraform-provider-aws_5.31.0_linux.zip",          // missing arch
		"provider-aws_5.31.0_linux_amd64.zip",              // missing prefix
		"terraform-provider-_5.31.0_linux_amd64.zip",       // empty type
		"",
	}
	for _, filename := range invalid {
		if _, _, _, _, err := parseTerraformProviderFilename(filename); err == nil {
			t.Errorf("%q: expected an error, got none", filename)
		}
	}
}

func TestValidateTerraformModuleIdentity(t *testing.T) {
	if err := validateTerraformModuleIdentity("vpc", "aws", "1.0.0"); err != nil {
		t.Errorf("valid identity rejected: %v", err)
	}

	cases := map[string][3]string{
		"missing name":     {"", "aws", "1.0.0"},
		"missing provider": {"vpc", "", "1.0.0"},
		"missing version":  {"vpc", "aws", ""},
		"non-semver":       {"vpc", "aws", "not-a-version"},
	}
	for label, c := range cases {
		if err := validateTerraformModuleIdentity(c[0], c[1], c[2]); err == nil {
			t.Errorf("%s: expected an error, got none", label)
		}
	}

	// semver.NewVersion is lenient: it coerces partial versions like "1.0" to
	// "1.0.0" rather than rejecting them. hc behaves identically, so accepting
	// these is deliberate parity. Switch to semver.StrictNewVersion to tighten.
	for _, v := range []string{"1.0", "1"} {
		if err := validateTerraformModuleIdentity("vpc", "aws", v); err != nil {
			t.Errorf("version %q: expected lenient acceptance, got %v", v, err)
		}
	}
}

func TestResolveTerraformFilePath(t *testing.T) {
	dir := t.TempDir()

	// exact path — exists
	tgz := filepath.Join(dir, "module.tar.gz")
	if err := os.WriteFile(tgz, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := resolveTerraformFilePath(tgz)
	if err != nil || got != tgz {
		t.Errorf("exact path: got (%q, %v), want (%q, nil)", got, err, tgz)
	}

	// exact path — does not exist
	if _, err := resolveTerraformFilePath(filepath.Join(dir, "missing.tar.gz")); err == nil {
		t.Error("missing file: expected error, got nil")
	}

	// glob — matches a .tar.gz
	got, err = resolveTerraformFilePath(filepath.Join(dir, "*.tar.gz"))
	if err != nil || got != tgz {
		t.Errorf("glob .tar.gz: got (%q, %v), want (%q, nil)", got, err, tgz)
	}

	// glob — matches files but none are terraform extensions
	txt := filepath.Join(dir, "readme.txt")
	if err := os.WriteFile(txt, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveTerraformFilePath(filepath.Join(dir, "*.txt")); err == nil {
		t.Error("glob non-terraform: expected error, got nil")
	}

	// glob — no match at all
	if _, err := resolveTerraformFilePath(filepath.Join(dir, "*.xyz")); err == nil {
		t.Error("glob no match: expected error, got nil")
	}

	// glob — picks .zip when both .zip and .txt exist
	zip := filepath.Join(dir, "provider.zip")
	if err := os.WriteFile(zip, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = resolveTerraformFilePath(filepath.Join(dir, "provider.*"))
	if err != nil || got != zip {
		t.Errorf("glob .zip: got (%q, %v), want (%q, nil)", got, err, zip)
	}
}

func TestDirHasRootTerraformFile(t *testing.T) {
	// .tf at the root → valid module directory
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.tf"), []byte("# module"), 0o644); err != nil {
		t.Fatal(err)
	}
	ok, err := dirHasRootTerraformFile(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("expected root .tf file to be detected")
	}

	// .tf only in a subdirectory → not a valid module root
	nested := t.TempDir()
	sub := filepath.Join(nested, "modules")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "main.tf"), []byte("# nested"), 0o644); err != nil {
		t.Fatal(err)
	}
	ok, err = dirHasRootTerraformFile(nested)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected nested-only .tf file to be rejected")
	}
}
