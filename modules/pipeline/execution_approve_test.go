// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package pipeline

import "testing"

func TestParseWaitingApprovals(t *testing.T) {
	body := []byte(`[
		{"id":"a1","type":"HarnessApproval","status":"WAITING","details":{"approvalMessage":"ship it?"}},
		{"id":"a2","type":"JiraApproval","status":"WAITING"},
		{"id":"a3","type":"HarnessApproval","status":"APPROVED"}
	]`)
	got, err := parseWaitingApprovals(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "a1" || got[0].Message != "ship it?" {
		t.Fatalf("got %+v", got)
	}
}

func TestPickApproval(t *testing.T) {
	for _, tc := range []struct {
		in      string
		want    int
		wantErr bool
	}{{"1", 0, false}, {" 2 ", 1, false}, {"0", 0, true}, {"3", 0, true}, {"x", 0, true}} {
		got, err := pickApproval(tc.in, 2)
		if (err != nil) != tc.wantErr || (!tc.wantErr && got != tc.want) {
			t.Errorf("pickApproval(%q) = %d, %v", tc.in, got, err)
		}
	}
}
