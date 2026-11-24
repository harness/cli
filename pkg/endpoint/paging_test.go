// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package endpoint

import (
	"context"
	"strings"
	"testing"

	"github.com/harness/cli/v3/pkg/auth"
	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/spec"
)

func TestBuildRequest_NoAccountID(t *testing.T) {
	tests := []struct {
		name string
		ep   *spec.EndpointSpec
		want bool
	}{
		{name: "default_false", ep: &spec.EndpointSpec{Path: "/items"}, want: false},
		{name: "propagated_true", ep: &spec.EndpointSpec{Path: "/items", NoAccountID: true}, want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := &cmdctx.Ctx{
				Context: context.Background(),
				Auth: &auth.ResolvedAuth{
					AccountID: "acct",
					OrgID:     "org",
					ProjectID: "proj",
				},
			}
			req, err := BuildRequest(ctx, tc.ep)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if req.NoAccountID != tc.want {
				t.Fatalf("req.NoAccountID = %v, want %v", req.NoAccountID, tc.want)
			}
		})
	}
}

type bodyFnResolver struct {
	cmdctx.Resolver
	fn cmdctx.CreateBodyFn
}

func (r bodyFnResolver) ResolveBodyFn(string) cmdctx.CreateBodyFn { return r.fn }
func (r bodyFnResolver) GetNoun(string) *spec.NounDef             { return nil }
func (r bodyFnResolver) GetSpec(string, string) *spec.CommandSpec { return nil }

func bodyFnCtx(fn cmdctx.CreateBodyFn) *cmdctx.Ctx {
	return &cmdctx.Ctx{
		Context:  context.Background(),
		Auth:     &auth.ResolvedAuth{AccountID: "acct"},
		Resolver: bodyFnResolver{fn: fn},
	}
}

func bodyPagingEP() *spec.EndpointSpec {
	return &spec.EndpointSpec{
		Method: "POST",
		Path:   "/items",
		BodyFn: "list_body",
		Paging: &spec.PagingSpec{
			PagingStrategy: "page_index",
			PageParamsIn:   spec.PageParamsInBody,
			PageSizeParam:  "pageSize",
			PageSizeMax:    50,
		},
	}
}

func TestBuildRequest_BodyFnFiltersSurviveBodyPaging(t *testing.T) {
	ctx := bodyFnCtx(func(*cmdctx.Ctx) (any, error) {
		return map[string]any{"filter": "foo"}, nil
	})
	ep := bodyPagingEP()
	req, err := BuildRequest(ctx, ep)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	strategy, _ := MakePagingStrategy(ep.Paging.PagingStrategy)
	strategy.InjectPaging(req, ep.Paging, 0, 20)
	body, _ := req.Body.(map[string]any)
	if body["filter"] != "foo" || body["pageIndex"] != 0 || body["pageSize"] != 20 {
		t.Fatalf("body = %v, want filter kept and paging keys added", req.Body)
	}
}

func TestHTTPFetchFn_BodyFnNonObjectRejectedForBodyPaging(t *testing.T) {
	for name, body := range map[string]any{
		"raw_body": &cmdctx.RawBody{ContentType: "application/yaml", Content: "a: b"},
		"array":    []any{"x"},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := bodyFnCtx(func(*cmdctx.Ctx) (any, error) { return body, nil })
			if _, err := HTTPFetchFn(ctx, bodyPagingEP(), 0, 20, nil); err == nil ||
				!strings.Contains(err.Error(), "JSON object request body") {
				t.Fatalf("err = %v, want JSON object request body error", err)
			}
		})
	}
}
