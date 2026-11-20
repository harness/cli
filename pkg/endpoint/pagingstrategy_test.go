// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package endpoint

import (
	"reflect"
	"testing"

	"github.com/harness/cli/v3/pkg/client"
	"github.com/harness/cli/v3/pkg/spec"
)

func TestPageIndexInjectPaging_BodyMode(t *testing.T) {
	tests := []struct {
		name      string
		start     int
		count     int
		wantIndex int
		wantSize  int
	}{
		{"count fits in one page", 0, 10, 0, 10},
		{"count one past the page boundary", 0, 11, 0, 10},
		{"short first page uses count as size", 5, 3, 0, 8},
		{"offset at the page boundary", 10, 5, 1, 10},
		{"offset one before the page boundary", 9, 2, 0, 10},
		{"offset deep in a later page", 25, 5, 2, 10},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pg := &spec.PagingSpec{
				PagingStrategy: spec.PagingStrategyPageIndex,
				PageParamsIn:   spec.PageParamsInBody,
				PageIndexParam: "pageIndex",
				PageSizeParam:  "pageSize",
				PageSizeMax:    10,
			}
			req := &client.Request{Body: map[string]any{
				"searchTerm": "keep",
				"pageIndex":  99,
				"pageSize":   "1000",
			}}

			pageIndexStrategy{}.InjectPaging(req, pg, tc.start, tc.count)

			want := map[string]any{"searchTerm": "keep", "pageIndex": tc.wantIndex, "pageSize": tc.wantSize}
			if !reflect.DeepEqual(req.Body, want) {
				t.Fatalf("body = %v, want %v", req.Body, want)
			}
			if len(req.QueryParams) != 0 {
				t.Fatalf("query params = %v, want none in body mode", req.QueryParams)
			}
		})
	}
}

func TestPageIndexInjectPaging_BodyModeNilBody(t *testing.T) {
	pg := &spec.PagingSpec{
		PagingStrategy: spec.PagingStrategyPageIndex,
		PageParamsIn:   spec.PageParamsInBody,
		PageSizeParam:  "pageSize",
		PageSizeMax:    10,
	}
	req := &client.Request{}

	pageIndexStrategy{}.InjectPaging(req, pg, 10, 5)

	want := map[string]any{"pageIndex": 1, "pageSize": 10}
	if !reflect.DeepEqual(req.Body, want) {
		t.Fatalf("body = %v, want %v (default pageIndex name)", req.Body, want)
	}
}

func TestPageIndexInjectPaging_QueryModeLeavesBodyAlone(t *testing.T) {
	pg := &spec.PagingSpec{
		PagingStrategy: spec.PagingStrategyPageIndex,
		PageIndexParam: "pageIndex",
		PageSizeParam:  "pageSize",
		PageSizeMax:    10,
	}
	req := &client.Request{Body: map[string]any{"searchTerm": "keep"}}

	pageIndexStrategy{}.InjectPaging(req, pg, 10, 5)

	if want := map[string]any{"searchTerm": "keep"}; !reflect.DeepEqual(req.Body, want) {
		t.Fatalf("body = %v, want %v", req.Body, want)
	}
	if want := map[string]string{"pageIndex": "1", "pageSize": "10"}; !reflect.DeepEqual(req.QueryParams, want) {
		t.Fatalf("query params = %v, want %v", req.QueryParams, want)
	}
}
