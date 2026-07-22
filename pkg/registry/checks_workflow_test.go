// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"strings"
	"testing"

	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/spec"
)

func TestCheckFunctions_WorkflowRegistered(t *testing.T) {
	r := New()
	wfID := "test:noop"
	r.RegisterWorkflow(wfID, func(*cmdctx.Ctx) error { return nil })
	cs := &spec.CommandSpec{
		Command:     "execute thing",
		Verb:        VerbExecute,
		Noun:        "thing",
		Module:      "test",
		HandlerType: spec.HandlerWorkflow,
		WorkflowID:  wfID,
	}
	if err := r.Register(cs); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := r.CheckFunctions(); err != nil {
		t.Fatalf("CheckFunctions() = %v, want nil", err)
	}
}

func TestCheckFunctions_WorkflowMissing(t *testing.T) {
	r := New()
	cs := &spec.CommandSpec{
		Command:     "execute thing",
		Verb:        VerbExecute,
		Noun:        "thing",
		Module:      "test",
		HandlerType: spec.HandlerWorkflow,
		WorkflowID:  "missing_handler",
	}
	if err := r.Register(cs); err != nil {
		t.Fatalf("Register: %v", err)
	}
	err := r.CheckFunctions()
	if err == nil {
		t.Fatal("CheckFunctions() = nil, want error")
	}
	if !strings.Contains(err.Error(), `workflow_id "missing_handler" not registered`) {
		t.Fatalf("CheckFunctions() error %q does not contain expected substring", err)
	}
}

func TestCheckFunctions_SkipsDevOnlyAndExternal(t *testing.T) {
	r := New()
	r.specs[VerbExecute] = append(r.specs[VerbExecute],
		&spec.CommandSpec{
			Command:     "execute devonly",
			Verb:        VerbExecute,
			Noun:        "devonly",
			Module:      "test",
			HandlerType: spec.HandlerWorkflow,
			WorkflowID:  "missing_handler",
			DevOnly:     true,
		},
		&spec.CommandSpec{
			Command:     "execute external",
			Verb:        VerbExecute,
			Noun:        "external",
			Module:      "test",
			HandlerType: spec.HandlerWorkflow,
			WorkflowID:  "missing_handler",
			External:    true,
		},
	)
	if err := r.CheckFunctions(); err != nil {
		t.Fatalf("CheckFunctions() = %v, want nil (DevOnly/External skipped)", err)
	}
}
