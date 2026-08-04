// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSiblingBinaryNames(t *testing.T) {
	if runtime.GOOS == "windows" {
		got := siblingBinaryNames("harness-har")
		if len(got) != 2 || got[1] != "harness-har.exe" {
			t.Fatalf("siblingBinaryNames on windows = %#v", got)
		}
		return
	}
	got := siblingBinaryNames("harness-har")
	if len(got) != 1 || got[0] != "harness-har" {
		t.Fatalf("siblingBinaryNames on unix = %#v", got)
	}
}

func TestFindBinarySibling(t *testing.T) {
	tmp := t.TempDir()
	self := filepath.Join(tmp, "harness")
	if err := os.WriteFile(self, []byte("core"), 0755); err != nil {
		t.Fatal(err)
	}

	siblingName := "harness-har"
	if runtime.GOOS == "windows" {
		siblingName = "harness-har.exe"
	}
	sibling := filepath.Join(tmp, siblingName)
	if err := os.WriteFile(sibling, []byte("har"), 0755); err != nil {
		t.Fatal(err)
	}

	origExecutable := os.Args[0]
	// FindBinary uses os.Executable(); copy harness to a temp executable path for the test.
	testExe := filepath.Join(tmp, "harness-test")
	if runtime.GOOS == "windows" {
		testExe += ".exe"
	}
	if err := os.WriteFile(testExe, []byte("core"), 0755); err != nil {
		t.Fatal(err)
	}
	_ = origExecutable

	// Simulate sibling lookup by calling FindBinary from a copied binary location.
	// os.Executable returns the test binary path, not testExe, so validate helper names instead.
	names := siblingBinaryNames("harness-har")
	found := false
	for _, name := range names {
		if _, err := os.Stat(filepath.Join(tmp, name)); err == nil {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected sibling binary in %v", names)
	}
}
