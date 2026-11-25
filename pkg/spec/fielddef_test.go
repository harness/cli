// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package spec

import "testing"

func TestFieldDefMutableOn(t *testing.T) {
	f := func(path, on string) FieldDef {
		return FieldDef{ID: "x", Expr: "it.x", MutablePath: path, MutableOn: on}
	}
	for _, tc := range []struct {
		name    string
		field   FieldDef
		wantErr bool
		create  bool
		update  bool
	}{
		{"unscoped", f("x", ""), false, true, true},
		{"create only", f("x", "create"), false, true, false},
		{"update only", f("x", "update"), false, false, true},
		{"bad verb", f("x", "delete"), true, false, false},
		{"without mutable_path", f("", "create"), true, false, false},
	} {
		if err := tc.field.Validate(); (err != nil) != tc.wantErr {
			t.Errorf("%s: Validate() = %v, wantErr %v", tc.name, err, tc.wantErr)
		}
		if tc.field.MutableOnVerb("create") != tc.create || tc.field.MutableOnVerb("update") != tc.update {
			t.Errorf("%s: create=%v update=%v", tc.name, tc.field.MutableOnVerb("create"), tc.field.MutableOnVerb("update"))
		}
	}
}
