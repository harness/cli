// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"testing"

	"github.com/harness/cli/modules/har/pkg/har/migrate/types"
)

// The OCI path prefix is "oci" on vanity-URL accounts and the lowercased
// accountID otherwise, so it has to come from the registry URL the API
// returned rather than being assumed.
func TestOCIImagePathUsesPrefixFromRegistryURL(t *testing.T) {
	cases := []struct {
		name        string
		registry    string
		cachedURL   string
		accountID   string
		wantPrefix  string
		description string
	}{
		{
			name:       "vanity url exposes an oci prefix",
			registry:   "helmoci",
			cachedURL:  "https://pkg.harness.io/oci/helmoci",
			accountID:  "AcctID123",
			wantPrefix: "oci",
		},
		{
			name:       "account-scoped url yields the account prefix",
			registry:   "dockertest",
			cachedURL:  "https://pkg.harness.io/abcdef123/dockertest",
			accountID:  "AbCdEf123",
			wantPrefix: "abcdef123",
		},
		{
			name:       "no cached url falls back to the lowercased accountID",
			registry:   "dockertest",
			cachedURL:  "",
			accountID:  "AbCdEf123",
			wantPrefix: "abcdef123",
		},
		{
			name:       "multi-segment prefix is preserved",
			registry:   "dockertest",
			cachedURL:  "https://pkg.harness.io/oci/nested/dockertest",
			accountID:  "acct",
			wantPrefix: "oci/nested",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &harAdapter{
				reg: types.RegistryConfig{
					Endpoint:  "https://pkg.harness.io",
					AccountID: tc.accountID,
				},
				registryURLCache: map[string]string{},
			}
			if tc.cachedURL != "" {
				a.registryURLCache[tc.registry] = tc.cachedURL
			}

			if got := a.ociPrefixFromCache(tc.registry); got != tc.wantPrefix {
				t.Errorf("ociPrefixFromCache() = %q, want %q", got, tc.wantPrefix)
			}

			got, err := a.GetOCIImagePath(tc.registry, "", "team/app")
			if err != nil {
				t.Fatalf("GetOCIImagePath() error: %v", err)
			}
			want := "pkg.harness.io/" + tc.wantPrefix + "/" + tc.registry + "/team/app"
			if got != want {
				t.Errorf("GetOCIImagePath() = %q, want %q", got, want)
			}
		})
	}
}
