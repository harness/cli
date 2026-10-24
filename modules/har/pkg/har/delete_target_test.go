// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/harness/cli/v3/pkg/auth"
	"github.com/harness/cli/v3/pkg/client"
	"github.com/harness/cli/v3/pkg/cmdctx"
)

func TestParseArtifactTarget(t *testing.T) {
	cases := []struct {
		id          string
		withVersion bool
		want        artifactTarget
		wantErr     bool
	}{
		{id: "reg/pkg", want: artifactTarget{Registry: "reg", Name: "pkg"}},
		{id: "reg/ns/mod/aws", want: artifactTarget{Registry: "reg", Name: "ns/mod/aws"}},
		{id: "reg/pkg/1.0.0", withVersion: true, want: artifactTarget{Registry: "reg", Name: "pkg", Version: "1.0.0"}},
		{id: "reg/ns/mod/aws/1.0.0", withVersion: true, want: artifactTarget{Registry: "reg", Name: "ns/mod/aws", Version: "1.0.0"}},
		{id: "reg", wantErr: true},
		{id: "reg/pkg", withVersion: true, wantErr: true},
		{id: "reg//pkg", wantErr: true},
		{id: "reg/pkg/", wantErr: true},
		{id: "/reg/pkg", wantErr: true},
	}
	for _, tc := range cases {
		got, err := parseArtifactTarget(tc.id, tc.withVersion)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseArtifactTarget(%q, %v) = %+v, want error", tc.id, tc.withVersion, got)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("parseArtifactTarget(%q, %v) = %+v, %v; want %+v", tc.id, tc.withVersion, got, err, tc.want)
		}
	}
}

func TestRunArtifactDelete_VerifiesRemoval(t *testing.T) {
	const wantPath = "/har/api/v1/registry/acct/org/proj/reg/+/artifact/ns/mod/aws/+/version/1.0.0"
	cases := []struct {
		name          string
		summaryStatus int
		wantErr       string
	}{
		{"read-back 404 means removed", http.StatusNotFound, ""},
		{"read-back 200 means still present", http.StatusOK, "still present"},
		{"read-back 500 is unconfirmed", http.StatusInternalServerError, "could not be confirmed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var deletedPath, deletedType, summaryType string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodDelete:
					deletedPath = r.URL.Path
					deletedType = r.URL.Query().Get("artifact_type")
					_, _ = w.Write([]byte(`{"status":"SUCCESS"}`))
				case strings.HasSuffix(r.URL.Path, "/+/artifacts"):
					_, _ = w.Write([]byte(`{"data":{"artifacts":[
						{"name":"ns/mod/aws","registryIdentifier":"reg","artifactType":"module"},
						{"name":"ns/mod/aws","registryIdentifier":"other","artifactType":"provider"}]}}`))
				default:
					summaryType = r.URL.Query().Get("artifact_type")
					w.WriteHeader(tc.summaryStatus)
					_, _ = w.Write([]byte(`{"status":"x"}`))
				}
			}))
			t.Cleanup(srv.Close)

			ctx := &cmdctx.Ctx{
				Context:    context.Background(),
				Id:         "reg/ns/mod/aws/1.0.0",
				FlagValues: map[string]any{"force": true},
				Auth: &auth.ResolvedAuth{
					AuthType:  auth.AuthTypePAT,
					APIUrl:    srv.URL,
					AccountID: "acct",
					OrgID:     "org",
					ProjectID: "proj",
					PATToken:  "test-token",
				},
			}
			err := runArtifactDelete(ctx, true)
			if deletedPath != wantPath {
				t.Fatalf("DELETE path = %q, want %q", deletedPath, wantPath)
			}
			if deletedType != "module" || summaryType != "module" {
				t.Fatalf("artifact_type on DELETE = %q, on read-back = %q; want module on both", deletedType, summaryType)
			}
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestRunArtifactDelete_ArtifactTypeResolution(t *testing.T) {
	cases := []struct {
		name         string
		flagType     string
		wantLookups  int
		wantDeleteAs string
		wantErr      string
	}{
		{name: "ambiguous name is refused before deleting", wantLookups: 1, wantErr: "more than one artifact type"},
		{name: "explicit type skips the lookup", flagType: "dataset", wantDeleteAs: "dataset"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lookups, deletes := 0, 0
			var deletedType string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodDelete:
					deletes++
					deletedType = r.URL.Query().Get("artifact_type")
					_, _ = w.Write([]byte(`{"status":"SUCCESS"}`))
				case strings.HasSuffix(r.URL.Path, "/+/artifacts"):
					lookups++
					_, _ = w.Write([]byte(`{"data":{"artifacts":[
						{"name":"org/m","registryIdentifier":"reg","artifactType":"model"},
						{"name":"org/m","registryIdentifier":"reg","artifactType":"dataset"}]}}`))
				default:
					w.WriteHeader(http.StatusNotFound)
					_, _ = w.Write([]byte(`{"status":"x"}`))
				}
			}))
			t.Cleanup(srv.Close)

			flags := map[string]any{"force": true}
			if tc.flagType != "" {
				flags["artifact-type"] = tc.flagType
			}
			ctx := &cmdctx.Ctx{
				Context:    context.Background(),
				Id:         "reg/org/m",
				FlagValues: flags,
				Auth: &auth.ResolvedAuth{
					AuthType: auth.AuthTypePAT, APIUrl: srv.URL, AccountID: "acct", PATToken: "test-token",
				},
			}
			err := runArtifactDelete(ctx, false)
			if lookups != tc.wantLookups {
				t.Fatalf("lookups = %d, want %d", lookups, tc.wantLookups)
			}
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) || deletes != 0 {
					t.Fatalf("error = %v, deletes = %d; want %q and no DELETE", err, deletes, tc.wantErr)
				}
				return
			}
			if err != nil || deletedType != tc.wantDeleteAs {
				t.Fatalf("error = %v, DELETE artifact_type = %q; want %q", err, deletedType, tc.wantDeleteAs)
			}
		})
	}
}

func TestLookupArtifactType_PagesUntilExactMatch(t *testing.T) {
	var pages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		pages = append(pages, page)
		rows := make([]string, 0, 100)
		if page == "0" {
			for i := 0; i < 100; i++ {
				rows = append(rows, `{"name":"ns/mod/aws-x","registryIdentifier":"reg","artifactType":"module"}`)
			}
		} else {
			rows = append(rows, `{"name":"ns/mod/aws","registryIdentifier":"reg","artifactType":"provider"}`)
		}
		_, _ = w.Write([]byte(`{"data":{"pageCount":2,"artifacts":[` + strings.Join(rows, ",") + `]}}`))
	}))
	t.Cleanup(srv.Close)

	ctx := &cmdctx.Ctx{
		Context: context.Background(),
		Auth:    &auth.ResolvedAuth{AuthType: auth.AuthTypePAT, APIUrl: srv.URL, AccountID: "acct", PATToken: "test-token"},
	}
	got, err := lookupArtifactType(client.New(ctx), "acct", artifactTarget{Registry: "reg", Name: "ns/mod/aws"}, nil)
	if err != nil || got != "provider" {
		t.Fatalf("lookupArtifactType = %q, %v; want provider from the second page", got, err)
	}
	if strings.Join(pages, ",") != "0,1" {
		t.Fatalf("pages requested = %v, want 0,1", pages)
	}
}

func TestRunArtifactDelete_PreviewRequestSendsNothing(t *testing.T) {
	withStdinTerminal(t, false)
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		_, _ = w.Write([]byte(`{"data":{"artifacts":[{"name":"ns/mod/aws","registryIdentifier":"reg","artifactType":"module"}]}}`))
	}))
	t.Cleanup(srv.Close)

	var preview strings.Builder
	ctx := &cmdctx.Ctx{
		Context:        context.Background(),
		Id:             "reg/ns/mod/aws",
		FlagValues:     map[string]any{},
		RequestPreview: &preview,
		Auth:           &auth.ResolvedAuth{AuthType: auth.AuthTypePAT, APIUrl: srv.URL, AccountID: "acct", PATToken: "test-token"},
	}
	if err := runArtifactDelete(ctx, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Join(methods, ",") != "GET" {
		t.Fatalf("requests sent = %v, want only the type lookup GET", methods)
	}
	if !strings.HasPrefix(preview.String(), "DELETE ") || !strings.Contains(preview.String(), "artifact_type=module") {
		t.Fatalf("preview = %q, want the DELETE with artifact_type=module", preview.String())
	}
}

func TestRunArtifactDelete_NoTTYWithoutForce(t *testing.T) {
	withStdinTerminal(t, false)
	ctx := &cmdctx.Ctx{Context: context.Background(), Id: "reg/pkg", FlagValues: map[string]any{}}
	if err := runArtifactDelete(ctx, false); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("error = %v, want a --force hint", err)
	}
}
