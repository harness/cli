// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
)

type testResource string

func (r testResource) String() string      { return string(r) }
func (r testResource) RegistryStr() string { return string(r) }

func TestHarKeychainResolve(t *testing.T) {
	const host = "pkg.harness.io"

	cases := []struct {
		name         string
		username     string
		password     string
		resource     string
		wantAnon     bool
		wantUsername string
	}{
		{
			name:         "username and password authenticate",
			username:     "user@example.com",
			password:     "pat.token",
			resource:     host,
			wantUsername: "user@example.com",
		},
		// Config validation accepts a token with no username, so this shape
		// must authenticate rather than silently degrade to anonymous.
		{
			name:         "password only falls back to the placeholder username",
			password:     "pat.token",
			resource:     host,
			wantUsername: "x-token",
		},
		{
			name:     "no password is anonymous",
			username: "user@example.com",
			resource: host,
			wantAnon: true,
		},
		{
			name:     "credentials are not sent to a different host",
			username: "user@example.com",
			password: "pat.token",
			resource: "other.example.com",
			wantAnon: true,
		},
		{
			name:         "host match is case-insensitive",
			username:     "user@example.com",
			password:     "pat.token",
			resource:     "PKG.HARNESS.IO",
			wantUsername: "user@example.com",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kc := NewHarKeychain(tc.username, tc.password, host)

			got, err := kc.Resolve(testResource(tc.resource))
			if err != nil {
				t.Fatalf("Resolve() error: %v", err)
			}

			if tc.wantAnon {
				if got != authn.Anonymous {
					t.Fatalf("Resolve() = %#v, want Anonymous", got)
				}
				return
			}

			if got == authn.Anonymous {
				t.Fatal("Resolve() = Anonymous, want credentials")
			}
			cfg, err := got.Authorization()
			if err != nil {
				t.Fatalf("Authorization() error: %v", err)
			}
			if cfg.Username != tc.wantUsername {
				t.Errorf("username = %q, want %q", cfg.Username, tc.wantUsername)
			}
			if cfg.Password != tc.password {
				t.Errorf("password = %q, want %q", cfg.Password, tc.password)
			}
		})
	}
}
