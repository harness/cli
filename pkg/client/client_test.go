// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
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

func TestBuildRequest_PipelineHeadersReplaceNormalAuthHeader(t *testing.T) {
	c := testClient("https://example.test")
	c.resolved.Headers = map[string]string{"x-api-key": "CIManager tok123"}

	req, _, err := c.buildRequest(Request{Method: "GET", Path: "/items"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := req.Header.Get("x-api-key"); got != "CIManager tok123" {
		t.Errorf("x-api-key = %q, want %q", got, "CIManager tok123")
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q, want empty — pipeline headers must fully replace, not stack", got)
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

func TestDoRequest_APIErrorFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte(`{"message":"boom"}`))
	}))
	defer srv.Close()

	_, _, err := testClient(srv.URL).Get("/p", nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("want *APIError, got %T: %v", err, err)
	}
	if err.Error() != "API error 500: boom" {
		t.Errorf("Error() = %q, callers match on the %q prefix", err.Error(), "API error ")
	}
	if apiErr.Method != "GET" || !strings.Contains(apiErr.URL, "/p") || string(apiErr.Body) != `{"message":"boom"}` {
		t.Errorf("apiErr = %+v", apiErr)
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestLogCorrelationIDsNeverPanics(t *testing.T) {
	h := http.Header{}
	h.Set("X-Harness-Trace-Id", "abc123")
	cases := []struct {
		name string
		h    http.Header
		body []byte
	}{
		{"nil header and body", nil, nil},
		{"header only", h, nil},
		{"body with correlationId", nil, []byte(`{"correlationId":"c-1"}`)},
		{"both", h, []byte(`{"correlationId":"c-1"}`)},
		{"non-JSON body", h, []byte("<html>oops</html>")},
		{"JSON array body", h, []byte(`[1,2,3]`)},
		{"wrong type correlationId", h, []byte(`{"correlationId":42}`)},
		{"empty JSON", nil, []byte(`{}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logCorrelationIDs("GET", "/v1/x", tc.h, tc.body)
		})
	}
}
