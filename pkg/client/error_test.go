// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package client

import "testing"

func TestAPIErrorMessage_ProblemJSONUsesDetail(t *testing.T) {
	body := []byte(`{"type":"https://developer.harness.io/docs/api-reference/errors#has-dependents","status":400,"title":"Resource Has Dependents","errorType":"hasDependents","detail":"Environment d264a970-9756-11f1-87f2-ca24eb84ddac cannot be archived, it has apitokens associated [tok-1]"}`)
	got := APIErrorMessage(400, body)
	if want := "Environment d264a970-9756-11f1-87f2-ca24eb84ddac cannot be archived, it has apitokens associated [tok-1]"; got != want {
		t.Fatalf("APIErrorMessage = %q, want detail %q", got, want)
	}
}

func TestAPIErrorMessage_PrefersDetailOverMessage(t *testing.T) {
	body := []byte(`{"message":"generic","detail":"specific dependent list"}`)
	got := APIErrorMessage(400, body)
	if got != "specific dependent list" {
		t.Fatalf("APIErrorMessage = %q, want detail", got)
	}
}

func TestAPIErrorMessage_FallsBackToMessage(t *testing.T) {
	body := []byte(`{"message":"legacy harness error"}`)
	got := APIErrorMessage(400, body)
	if got != "legacy harness error" {
		t.Fatalf("APIErrorMessage = %q, want message", got)
	}
}

func TestAPIErrorMessage_DoesNotTruncateDetail(t *testing.T) {
	detail := "Environment abc cannot be archived, it has apitokens associated [" +
		"tok-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee, tok-ffffffff-1111-2222-3333-444444444444]"
	body := []byte(`{"errorType":"hasDependents","detail":"` + detail + `"}`)
	got := APIErrorMessage(400, body)
	if got != detail {
		t.Fatalf("truncated or rewritten detail: %q", got)
	}
}
