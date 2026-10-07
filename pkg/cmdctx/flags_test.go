// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package cmdctx

import "testing"

func TestSetFlagAndRemoveFlag(t *testing.T) {
	ctx := &Ctx{}
	ctx.SetFlag("comment", "")
	if value, exists := ctx.FlagValues["comment"]; !exists || value != "" {
		t.Fatalf("FlagValues[comment] = (%v, %t), want (empty string, true)", value, exists)
	}
	if !ctx.ProvidedFlags["comment"] {
		t.Fatal("SetFlag should mark an empty value as provided")
	}
	ctx.SetFlag("enabled", false)
	if value, exists := ctx.FlagValues["enabled"]; !exists || value != false || !ctx.ProvidedFlags["enabled"] {
		t.Fatalf("explicit false flag = (%v, %t), want (false, true)", value, exists)
	}
	ctx.RemoveFlag("comment")
	if _, exists := ctx.FlagValues["comment"]; exists {
		t.Error("RemoveFlag left the flag value behind")
	}
	if _, exists := ctx.ProvidedFlags["comment"]; exists {
		t.Error("RemoveFlag left the presence entry behind")
	}
	if !ctx.ProvidedFlags["enabled"] {
		t.Error("RemoveFlag removed another flag's presence")
	}
	ctx.RemoveFlag("missing")
	(&Ctx{}).RemoveFlag("missing")
}
