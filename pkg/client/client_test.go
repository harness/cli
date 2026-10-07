// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/harness/cli/v3/pkg/auth"
)

func testClient(apiURL string) *Client {
	return &Client{
		ctx: context.Background(),
		resolved: &auth.ResolvedAuth{
			APIUrl:    apiURL,
			AccountID: "acct",
			AuthType:  auth.AuthTypePAT,
			PATToken:  "pat.test",
		},
		http: &http.Client{},
	}
}

func TestBuildRequest_AccountIdentifier(t *testing.T) {
	tests := []struct {
		name           string
		reqNoAccountID bool
		clientNoAcctID bool
		wantPresent    bool
	}{
		{name: "default_present", wantPresent: true},
		{name: "request_suppressed", reqNoAccountID: true, wantPresent: false},
		{name: "client_suppressed", clientNoAcctID: true, wantPresent: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := testClient("https://example.test")
			c.NoAccountID = tc.clientNoAcctID
			req, u, err := c.buildRequest(Request{Method: "GET", Path: "/items", NoAccountID: tc.reqNoAccountID})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			_ = req
			got := u.Query().Get("accountIdentifier") != ""
			if got != tc.wantPresent {
				t.Fatalf("accountIdentifier present = %v, want %v (query=%q)", got, tc.wantPresent, u.RawQuery)
			}
		})
	}
}

func TestDoRequest_AccountIdentifierOmittedOnWire(t *testing.T) {
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := testClient(srv.URL)
	c.NoAccountID = true
	if _, _, err := c.DoRequest(Request{Method: "GET", Path: "/items"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := gotQuery.Get("accountIdentifier"); got != "" {
		t.Fatalf("accountIdentifier = %q, want empty", got)
	}
}
