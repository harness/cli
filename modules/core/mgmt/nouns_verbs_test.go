// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package mgmt

import (
	"testing"

	"github.com/harness/cli/v3/pkg/spec"
)

func TestEditableNote(t *testing.T) {
	cmds := map[string]*nounCmd{
		"create": {cs: &spec.CommandSpec{BuiltinFlags: spec.BuiltinFlags{Set: true}}},
		"update": {cs: &spec.CommandSpec{BuiltinFlags: spec.BuiltinFlags{Set: true}}},
	}
	f := func(on, typ string) spec.FieldDef {
		return spec.FieldDef{ID: "x", MutablePath: "x", MutableOn: on, FieldType: typ}
	}
	for _, tc := range []struct {
		name  string
		field spec.FieldDef
		cmds  map[string]*nounCmd
		want  string
	}{
		{"both verbs", f("", ""), cmds, "editable: create, update; scalar"},
		{"create only", f("create", "bool"), cmds, "editable: create; bool"},
		{"command lacks --set", f("", ""), map[string]*nounCmd{"update": {cs: &spec.CommandSpec{}}}, "editable"},
		{"read only", spec.FieldDef{ID: "x"}, cmds, ""},
	} {
		if got := editableNote(tc.field, tc.cmds); got != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}
