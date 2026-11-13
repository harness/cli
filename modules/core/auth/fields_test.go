// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"strings"
	"testing"

	"github.com/harness/cli/v3/pkg/auth"
)

func TestFieldValue(t *testing.T) {
	pat := &auth.ResolvedAuth{PATToken: "pat.x", Email: "a@b.io"}
	sso := &auth.ResolvedAuth{AuthType: auth.AuthTypeSSO, SSOToken: "jwt", Email: "a@b.io"}

	tests := []struct {
		name    string
		r       *auth.ResolvedAuth
		field   string
		want    string
		wantErr string
	}{
		{"value", pat, "email", "a@b.io", ""},
		{"sso jwt is never returned as token", sso, "token", "", "not set"},
		{"unset field", pat, "org", "", "not set"},
		{"missing name", pat, "", "", "usage:"},
		{"unknown name", pat, "nope", "", "usage:"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := fieldValue(tt.r, tt.field)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("got %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}
