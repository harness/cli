// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import "testing"

func TestParseCargoToml(t *testing.T) {
	t.Run("valid TOML the line parser rejected", func(t *testing.T) {
		data := []byte(`
[dependencies]
name = "not-this-one"

[package]
description = """
multi
line
"""
authors = ["a"]
name = "my-crate"
version = "1.2.3"
`)
		name, version, err := parseCargoToml(data)
		if err != nil || name != "my-crate" || version != "1.2.3" {
			t.Fatalf("parseCargoToml = (%q, %q, %v), want (my-crate, 1.2.3, nil)", name, version, err)
		}
	})

	t.Run("dotted package keys", func(t *testing.T) {
		name, version, err := parseCargoToml([]byte("package.name = \"c\"\npackage.version = \"0.1.0\"\n"))
		if err != nil || name != "c" || version != "0.1.0" {
			t.Fatalf("parseCargoToml = (%q, %q, %v), want (c, 0.1.0, nil)", name, version, err)
		}
	})

	t.Run("missing version errors", func(t *testing.T) {
		if _, _, err := parseCargoToml([]byte("[package]\nname = \"c\"\n")); err == nil {
			t.Fatal("want error for missing version")
		}
	})

	t.Run("malformed TOML errors", func(t *testing.T) {
		if _, _, err := parseCargoToml([]byte("[package\nname = ")); err == nil {
			t.Fatal("want error for malformed TOML")
		}
	})
}
