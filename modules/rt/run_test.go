// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package rt

import (
	"strings"
	"testing"

	"github.com/harness/cli/pkg/auth"
	"github.com/harness/cli/pkg/cmdctx"
)

func TestSetArgsToValues(t *testing.T) {
	got := setArgsToValues(map[string]string{
		"users":    "500",
		"duration": "10m",
		"host":     "api.example.com",
	})
	want := []map[string]string{
		{"name": "duration", "value": "10m"},
		{"name": "host", "value": "api.example.com"},
		{"name": "users", "value": "500"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d values, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i]["name"] != want[i]["name"] || got[i]["value"] != want[i]["value"] {
			t.Errorf("value %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestSetArgsToValuesEmpty(t *testing.T) {
	if got := setArgsToValues(nil); got != nil {
		t.Errorf("got %v, want nothing sent when no --set is given", got)
	}
	if got := setArgsToValues(map[string]string{}); got != nil {
		t.Errorf("got %v, want nothing sent for an empty set", got)
	}
}

func TestRandomSuffix(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		s := randomSuffix()
		if len(s) != runSuffixLen {
			t.Fatalf("suffix %q is %d characters, want %d", s, len(s), runSuffixLen)
		}
		for _, r := range s {
			if !strings.ContainsRune(runSuffixAlphabet, r) {
				t.Fatalf("suffix %q holds %q, which is outside the console's alphabet", s, r)
			}
		}
		seen[s] = true
	}
	// 36^3 possibilities, so 200 draws collapsing to a handful means the draw is broken.
	if len(seen) < 100 {
		t.Errorf("200 draws produced only %d distinct suffixes", len(seen))
	}
}

func TestStartRunBodyNeedsID(t *testing.T) {
	_, err := startRunBody(&cmdctx.Ctx{})
	if err == nil || !strings.Contains(err.Error(), "<loadtest-id>") {
		t.Fatalf("expected the missing-id message, got %v", err)
	}
}

func TestStartRunBodyPicksAnUntakenIdentity(t *testing.T) {
	ctx, calls := apiCtx(t, map[string]any{
		api("/load-tests/checkout/runs"): runList("checkout-aaa", "checkout-bbb"),
	})
	ctx.Id = "checkout"

	body, err := startRunBody(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	identity, _ := asMap(body)["identity"].(string)
	if identity == "checkout-aaa" || identity == "checkout-bbb" {
		t.Errorf("identity %q is already taken", identity)
	}
	if !strings.HasPrefix(identity, "checkout-") || len(identity) != len("checkout-")+runSuffixLen {
		t.Errorf("identity = %q, want checkout- and %d more characters", identity, runSuffixLen)
	}

	// Without the limit the scan sees a default page and calls a used suffix free.
	c, ok := findCall(calls, "GET", api("/load-tests/checkout/runs"))
	if !ok {
		t.Fatal("the existing runs were never read")
	}
	if c.query.Get("limit") == "" {
		t.Error("the run scan should ask for a page large enough to be worth reading")
	}
}

func TestStartRunBodySurvivesAnUnreadableRunList(t *testing.T) {
	ctx, _ := apiCtx(t, nil) // every route 404s
	ctx.Id = "checkout"

	body, err := startRunBody(ctx)
	if err != nil {
		t.Fatalf("a failed lookup should not stop a run: %v", err)
	}
	if identity, _ := asMap(body)["identity"].(string); !strings.HasPrefix(identity, "checkout-") {
		t.Errorf("identity = %q, want one derived from the load test anyway", identity)
	}
}

func TestStartRunBodyCarriesNameAndOverrides(t *testing.T) {
	ctx, _ := apiCtx(t, map[string]any{api("/load-tests/checkout/runs"): runList()})
	ctx.Id = "checkout"
	ctx.FlagValues["name"] = "peak traffic"
	ctx.SetArgs = map[string]string{"users": "500"}

	body, err := startRunBody(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := asMap(body)
	if m["name"] != "peak traffic" {
		t.Errorf("name = %v, want it carried through", m["name"])
	}
	values, _ := m["values"].([]map[string]string)
	if len(values) != 1 || values[0]["name"] != "users" || values[0]["value"] != "500" {
		t.Errorf("values = %v, want the one --set override", m["values"])
	}
}

func TestRerunNeedsARunID(t *testing.T) {
	ctx, _ := apiCtx(t, nil)
	if err := rerunWorkflow(ctx); err == nil || !strings.Contains(err.Error(), "<run-id>") {
		t.Fatalf("expected the missing-id message, got %v", err)
	}
}

func TestRerunStartsAFreshRunOfTheSameLoadTest(t *testing.T) {
	ctx, calls := apiCtx(t, map[string]any{
		api("/runs/checkout-aaa"):        map[string]any{"identity": "checkout-aaa", "loadTestIdentity": "checkout"},
		api("/load-tests/checkout/runs"): map[string]any{"identity": "checkout-bbb"},
	})
	ctx.Id = "checkout-aaa"

	if err := rerunWorkflow(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	post, ok := findCall(calls, "POST", api("/load-tests/checkout/runs"))
	if !ok {
		t.Fatal("no run was started against the parent load test")
	}
	// Unnamed, a rerun is indistinguishable from the original in a list.
	if name, _ := post.body["name"].(string); !strings.Contains(name, "checkout-aaa") {
		t.Errorf("name = %q, want the run it reruns named", name)
	}
	// Callers reuse ctx after a workflow returns.
	if ctx.Id != "checkout-aaa" {
		t.Errorf("ctx.Id = %q, want the previous run restored", ctx.Id)
	}
}

func TestRerunKeepsAnExplicitName(t *testing.T) {
	ctx, calls := apiCtx(t, map[string]any{
		api("/runs/checkout-aaa"):        map[string]any{"loadTestIdentity": "checkout"},
		api("/load-tests/checkout/runs"): map[string]any{"identity": "checkout-bbb"},
	})
	ctx.Id = "checkout-aaa"
	ctx.FlagValues["name"] = "friday peak"

	if err := rerunWorkflow(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	post, _ := findCall(calls, "POST", api("/load-tests/checkout/runs"))
	if post.body["name"] != "friday peak" {
		t.Errorf("name = %v, want --name to win over the default", post.body["name"])
	}
}

func TestRerunRefusesAnInternalParentID(t *testing.T) {
	ctx, calls := apiCtx(t, map[string]any{
		api("/runs/checkout-aaa"): map[string]any{
			"loadTestIdentity": "1b4e28ba-2fa1-11d2-883f-0016d3cca427",
		},
	})
	ctx.Id = "checkout-aaa"

	err := rerunWorkflow(ctx)
	if err == nil {
		t.Fatal("expected an internal parent id to be refused")
	}
	if !strings.Contains(err.Error(), "harness execute loadtest") {
		t.Errorf("error %q should name the command that does work", err)
	}
	if len(*calls) != 1 {
		t.Errorf("made %d requests, want the rerun refused before starting anything", len(*calls))
	}
}

func TestRerunRefusesARunWithNoParent(t *testing.T) {
	ctx, _ := apiCtx(t, map[string]any{
		api("/runs/checkout-aaa"): map[string]any{"identity": "checkout-aaa"},
	})
	ctx.Id = "checkout-aaa"

	err := rerunWorkflow(ctx)
	if err == nil || !strings.Contains(err.Error(), "cannot be rerun") {
		t.Fatalf("expected a run with no recorded parent to be refused, got %v", err)
	}
}

func TestRerunReportsAnUnreadablePreviousRun(t *testing.T) {
	ctx, _ := apiCtx(t, nil)
	ctx.Id = "checkout-aaa"

	err := rerunWorkflow(ctx)
	if err == nil || !strings.Contains(err.Error(), "checkout-aaa") {
		t.Fatalf("expected the unreadable run named in the error, got %v", err)
	}
}

func TestUUIDPattern(t *testing.T) {
	for _, id := range []string{
		"1b4e28ba-2fa1-11d2-883f-0016d3cca427",
		"1B4E28BA-2FA1-11D2-883F-0016D3CCA427",
	} {
		if !uuidPattern.MatchString(id) {
			t.Errorf("%q should be recognised as an internal id", id)
		}
	}
	for _, id := range []string{
		"checkout-load",
		"checkout",
		"1b4e28ba-2fa1-11d2-883f",
		"1b4e28ba2fa111d2883f0016d3cca427",
		"",
	} {
		if uuidPattern.MatchString(id) {
			t.Errorf("%q is a name, not an internal id", id)
		}
	}
}

func TestScopeParams(t *testing.T) {
	got := scopeParams(&cmdctx.Ctx{Auth: &auth.ResolvedAuth{
		AccountID: "acct", OrgID: "eng", ProjectID: "payments",
	}})
	if got["organizationIdentifier"] != "eng" || got["projectIdentifier"] != "payments" {
		t.Errorf("got %v, want the org and project carried through", got)
	}
	if _, present := got["accountIdentifier"]; present {
		t.Error("accountIdentifier is the client's to add")
	}

	got = scopeParams(&cmdctx.Ctx{Auth: &auth.ResolvedAuth{AccountID: "acct", OrgID: "eng"}})
	if got["organizationIdentifier"] != "eng" {
		t.Errorf("got %v, want the org", got)
	}
	if _, present := got["projectIdentifier"]; present {
		t.Error("an account-level scope should not send an empty project")
	}

	if got := scopeParams(&cmdctx.Ctx{}); len(got) != 0 {
		t.Errorf("got %v, want nothing when there is no auth", got)
	}
}

func TestResponseFieldHelpers(t *testing.T) {
	m := map[string]any{"name": "checkout", "rps": 12.5, "count": float64(7)}
	if stringField(m, "name") != "checkout" {
		t.Error("stringField should read a string")
	}
	if floatField(m, "rps") != 12.5 || floatField(m, "count") != 7 {
		t.Error("floatField should read both rates and counts")
	}

	// Wrong type, absent field and nil object all mean "not reported", not a panic.
	if stringField(m, "rps") != "" || stringField(m, "absent") != "" || stringField(nil, "name") != "" {
		t.Error("stringField should be empty for anything it cannot read")
	}
	if floatField(m, "name") != 0 || floatField(m, "absent") != 0 || floatField(nil, "rps") != 0 {
		t.Error("floatField should be zero for anything it cannot read")
	}

	if asMap(map[string]any{"a": 1})["a"] != 1 {
		t.Error("asMap should pass an object through")
	}
	for _, v := range []any{nil, "text", 42, []any{1}} {
		if asMap(v) != nil {
			t.Errorf("asMap(%v) should be nil for a non-object", v)
		}
	}
}
