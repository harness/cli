// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"bytes"
	"strings"
	"testing"

	"github.com/harness/cli/v3/pkg/auth"
)

func TestPrintFields(t *testing.T) {
	pat := &auth.ResolvedAuth{PATToken: "pat.x", Email: "a@b.io", APIUrl: "https://app", AccountID: "acc"}
	sso := &auth.ResolvedAuth{AuthType: auth.AuthTypeSSO, SSOToken: "jwt", Email: "a@b.io"}

	tests := []struct {
		name    string
		r       *auth.ResolvedAuth
		field   string
		want    string
		wantErr string
	}{
		{"all omits unset fields", pat, "", "token=pat.x\nemail=a@b.io\napi_url=https://app\naccount=acc\n", ""},
		{"single field is raw value", pat, "email", "a@b.io\n", ""},
		{"sso never exposes its jwt as token", sso, "token", "", `field "token" is not set`},
		{"unset field errors", pat, "org", "", `field "org" is not set`},
		{"unknown field lists valid names", pat, "nope", "", "valid: token, email"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := printFields(&buf, tt.r, tt.field)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if buf.String() != tt.want {
				t.Fatalf("got %q, want %q", buf.String(), tt.want)
			}
		})
	}
}
