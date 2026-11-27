// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build nexus_integration

package nexus

import (
	"context"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	adp "github.com/harness/cli/modules/har/pkg/har/migrate/adapter"
	"github.com/harness/cli/modules/har/pkg/har/migrate/types"
)

// Read-only comparison of the sequential and parallel listings against a real Nexus:
//
//	NEXUS_IT_URL=http://localhost:8081 NEXUS_IT_USER=<user> NEXUS_IT_PASSWORD=<password> \
//	NEXUS_IT_REPOS=maven-releases,maven-snapshots NEXUS_IT_CONCURRENCY=8 \
//	go test -tags nexus_integration -run TestNexusParallelListingMatchesSequential -v ./pkg/har/migrate/adapter/nexus/
func integrationClient(t *testing.T) *client {
	t.Helper()
	endpoint := os.Getenv("NEXUS_IT_URL")
	if endpoint == "" {
		t.Skip("NEXUS_IT_URL not set")
	}
	return newClient(&types.RegistryConfig{
		Endpoint: endpoint,
		Credentials: types.CredentialsConfig{
			Username: os.Getenv("NEXUS_IT_USER"),
			Password: os.Getenv("NEXUS_IT_PASSWORD"),
		},
	})
}

func integrationConcurrency(t *testing.T) int {
	n, err := strconv.Atoi(os.Getenv("NEXUS_IT_CONCURRENCY"))
	if err != nil || n <= 0 {
		return 8
	}
	return n
}

// assetKeys identifies a listing by its asset paths, which is what gets migrated.
func assetKeys(components []NexusComponent) []string {
	var keys []string
	for _, c := range components {
		for _, a := range c.Assets {
			keys = append(keys, c.Group+":"+c.Name+":"+c.Version+" "+a.Path)
		}
	}
	slices.Sort(keys)
	return keys
}

func TestNexusParallelListingMatchesSequential(t *testing.T) {
	c := integrationClient(t)
	concurrency := integrationConcurrency(t)
	for _, repo := range strings.Split(os.Getenv("NEXUS_IT_REPOS"), ",") {
		t.Run(repo, func(t *testing.T) {
			started := time.Now()
			sequential, err := c.listComponents(context.Background(), repo, adp.ListOptions{})
			if err != nil {
				t.Fatalf("sequential listing: %v", err)
			}
			seqTook := time.Since(started)

			started = time.Now()
			parallel, err := c.listComponents(context.Background(), repo, adp.ListOptions{
				Concurrency: concurrency,
				Progress:    func(s string) { t.Log(s) },
			})
			if err != nil {
				t.Fatalf("parallel listing: %v", err)
			}
			parTook := time.Since(started)

			t.Logf("sequential: %d components in %s; parallel (%d): %d components in %s",
				len(sequential), seqTook.Round(time.Millisecond), concurrency, len(parallel), parTook.Round(time.Millisecond))

			want, got := assetKeys(sequential), assetKeys(parallel)
			missing, extra := diffSorted(want, got)
			for _, k := range missing {
				t.Errorf("missing from parallel listing: %s", k)
			}
			for _, k := range extra {
				t.Errorf("only in parallel listing: %s", k)
			}
		})
	}
}

// TestNexusParallelListingDetectsTruncatedBrowse expects Nexus to run with a small
// nexus.browse.component.tree.maxHtmlNodes, so a folder in the repo is truncated.
func TestNexusParallelListingDetectsTruncatedBrowse(t *testing.T) {
	c := integrationClient(t)
	repo := os.Getenv("NEXUS_IT_TRUNCATED_REPO")
	if repo == "" {
		t.Skip("NEXUS_IT_TRUNCATED_REPO not set")
	}
	_, err := c.listComponents(context.Background(), repo, adp.ListOptions{Concurrency: integrationConcurrency(t)})
	if err == nil || !strings.Contains(err.Error(), "only part of this folder") {
		t.Fatalf("err = %v, want the truncated browse page detected", err)
	}
	t.Logf("got expected error: %v", err)
}

func diffSorted(want, got []string) (missing, extra []string) {
	i, j := 0, 0
	for i < len(want) || j < len(got) {
		switch {
		case j == len(got) || (i < len(want) && want[i] < got[j]):
			missing = append(missing, want[i])
			i++
		case i == len(want) || got[j] < want[i]:
			extra = append(extra, got[j])
			j++
		default:
			i++
			j++
		}
	}
	return missing, extra
}
