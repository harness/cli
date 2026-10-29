// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBackupFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	src := filepath.Join(t.TempDir(), ".npmrc")

	t.Run("missing file", func(t *testing.T) {
		got, err := backupFile(src, "npmrc")
		if err != nil || got != "" {
			t.Fatalf("got (%q, %v), want (\"\", nil)", got, err)
		}
	})

	t.Run("blank file", func(t *testing.T) {
		if err := os.WriteFile(src, []byte(" \n\t\n"), 0600); err != nil {
			t.Fatal(err)
		}
		got, err := backupFile(src, "npmrc")
		if err != nil || got != "" {
			t.Fatalf("got (%q, %v), want (\"\", nil)", got, err)
		}
	})

	t.Run("copies contents with 0600 and never overwrites a prior backup", func(t *testing.T) {
		want := "registry=https://example.invalid/\n"
		if err := os.WriteFile(src, []byte(want), 0644); err != nil {
			t.Fatal(err)
		}
		first, err := backupFile(src, "npmrc")
		if err != nil {
			t.Fatal(err)
		}
		second, err := backupFile(src, "npmrc")
		if err != nil {
			t.Fatal(err)
		}
		if first == second {
			t.Fatalf("second backup reused path %q", first)
		}
		for _, p := range []string{first, second} {
			data, err := os.ReadFile(p)
			if err != nil || string(data) != want {
				t.Errorf("backup %q = %q, %v", p, data, err)
			}
			if info, _ := os.Stat(p); info.Mode().Perm() != 0600 {
				t.Errorf("backup %q mode = %v, want 0600", p, info.Mode().Perm())
			}
		}
	})
}
