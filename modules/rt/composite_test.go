// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package rt

import (
	"reflect"
	"strings"
	"testing"

	"github.com/harness/cli/v3/pkg/cmdctx"
)

func TestCheckCompositeIdentifier(t *testing.T) {
	cases := []struct {
		name       string
		identifier string
		wantErr    string // substring; empty means the identifier is accepted
	}{
		{name: "plain", identifier: "checkout_load"},
		{name: "digits and dollar", identifier: "svc$2_peak"},
		{name: "single letter", identifier: "a"},
		{name: "mixed case", identifier: "CheckoutLoad"},

		{name: "empty", identifier: "", wantErr: "requires an <id>"},
		{name: "too long", identifier: strings.Repeat("a", 129), wantErr: "capped at 128"},

		// The defect: load test identities take hyphens, pipeline identifiers do not.
		{name: "hyphen suggests underscore", identifier: "checkout-load", wantErr: `try "checkout_load"`},
		{name: "dot suggests underscore", identifier: "checkout.load", wantErr: `try "checkout_load"`},
		{name: "space suggests underscore", identifier: "checkout load", wantErr: `try "checkout_load"`},
		{name: "slash suggests underscore", identifier: "checkout/load", wantErr: `try "checkout_load"`},

		// A rewrite is only offered when it would actually be accepted.
		{name: "leading digit", identifier: "2checkout", wantErr: "has to start with a letter"},
		{name: "leading underscore", identifier: "_checkout", wantErr: "has to start with a letter"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkCompositeIdentifier(tc.identifier)

			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("identifier %q: unexpected error: %v", tc.identifier, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("identifier %q: expected an error containing %q, got none", tc.identifier, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("identifier %q: error %q does not contain %q", tc.identifier, err, tc.wantErr)
			}
		})
	}
}

func TestCheckCompositeIdentifierOnlySuggestsValidRewrites(t *testing.T) {
	for _, identifier := range []string{"2-checkout", "-checkout", "...", "$checkout"} {
		err := checkCompositeIdentifier(identifier)
		if err == nil {
			t.Fatalf("identifier %q: expected rejection", identifier)
		}
		if strings.Contains(err.Error(), "try ") {
			t.Fatalf("identifier %q: offered a rewrite that would also be rejected: %v", identifier, err)
		}
	}
}

// The identifier is checked before the body is built, so a bad one never reaches the server.
func TestCompositeBodyRejectsBadIdentifier(t *testing.T) {
	for _, identifier := range []string{"", "checkout-load", "2checkout"} {
		if _, err := compositeBody(&cmdctx.Ctx{Id: identifier, FlagValues: map[string]any{}}); err == nil {
			t.Fatalf("identifier %q: expected rejection", identifier)
		}
	}
}

func TestCompositeBody(t *testing.T) {
	body, err := compositeBody(&cmdctx.Ctx{
		Id: "checkout_load",
		FlagValues: map[string]any{
			"name":           "Checkout peak",
			"description":    "Black Friday rehearsal",
			"objective":      "hold p95 under 400ms",
			"loadtest":       "checkout-peak",
			"probe":          "cart_latency",
			"probe-infra":    "prod_k8s",
			"probe-duration": "10m",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := map[string]any{
		"identifier":  "checkout_load",
		"name":        "Checkout peak",
		"description": "Black Friday rehearsal",
		"objective":   "hold p95 under 400ms",
		"loadTest":    map[string]any{"loadTestRef": "checkout-peak"},
		"probe": map[string]any{
			"identity":       "cart_latency",
			"infraReference": "prod_k8s",
			"duration":       "10m",
		},
	}
	if !reflect.DeepEqual(body, want) {
		t.Fatalf("body mismatch:\n got %#v\nwant %#v", body, want)
	}
}

// Every key stays present when its flag is unset — the route reads an absent probe
// block as no probe at all, rather than as one waiting on a runtime input.
func TestCompositeBodyDefaultsNameToIdentifierAndKeepsEmptyKeys(t *testing.T) {
	body, err := compositeBody(&cmdctx.Ctx{
		Id:         "checkout_load",
		FlagValues: map[string]any{"loadtest": "checkout-peak", "probe": "cart_latency"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := map[string]any{
		"identifier":  "checkout_load",
		"name":        "checkout_load",
		"description": "",
		"objective":   "",
		"loadTest":    map[string]any{"loadTestRef": "checkout-peak"},
		"probe": map[string]any{
			"identity":       "cart_latency",
			"infraReference": "",
			"duration":       "",
		},
	}
	if !reflect.DeepEqual(body, want) {
		t.Fatalf("body mismatch:\n got %#v\nwant %#v", body, want)
	}
}
