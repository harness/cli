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

func TestDoRequest_APIErrorDump(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     string
		wantBody any
		wantDump bool
	}{
		{"json body embedded as json", 500, `{"message":"boom","code":"X"}`, map[string]any{"message": "boom", "code": "X"}, true},
		{"html body kept as string", 502, `<html>bad gateway</html>`, `<html>bad gateway</html>`, true},
		{"empty body", 400, ``, ``, true},
		{"3xx has no dump", 304, `{"message":"x"}`, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Test", "hdr")
				w.Header().Add("Set-Cookie", "session=secret")
				w.WriteHeader(tt.status)
				w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			_, _, err := testClient(srv.URL).Get("/p", nil)
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("want *APIError, got %T: %v", err, err)
			}
			if !strings.HasPrefix(err.Error(), "API error ") {
				t.Errorf("Error() = %q, must keep the %q prefix callers match on", err.Error(), "API error ")
			}
			dump := apiErr.Dump()
			if (dump != "") != tt.wantDump {
				t.Fatalf("Dump() empty=%v, want dump=%v", dump == "", tt.wantDump)
			}
			if !tt.wantDump {
				return
			}
			var got struct {
				Request  struct{ Method, URL string }
				Response struct {
					Status  int
					Headers map[string][]string
					Body    any
				}
			}
			if err := json.Unmarshal([]byte(dump), &got); err != nil {
				t.Fatalf("dump is not valid JSON: %v\n%s", err, dump)
			}
			if got.Request.Method != "GET" || !strings.Contains(got.Request.URL, "/p") {
				t.Errorf("request = %+v", got.Request)
			}
			if strings.Contains(dump, "secret") || got.Response.Headers["Set-Cookie"][0] != "[redacted]" {
				t.Errorf("Set-Cookie not redacted: %v", got.Response.Headers["Set-Cookie"])
			}
			if got.Response.Status != tt.status || got.Response.Headers["X-Test"][0] != "hdr" {
				t.Errorf("response = %+v", got.Response)
			}
			if gb, _ := json.Marshal(got.Response.Body); string(gb) != mustJSON(tt.wantBody) {
				t.Errorf("body = %s, want %s", gb, mustJSON(tt.wantBody))
			}
		})
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
