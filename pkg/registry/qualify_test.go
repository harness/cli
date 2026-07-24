// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"strings"
	"testing"

	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/spec"
)

func noopWorkflow(*cmdctx.Ctx) error { return nil }

func TestQualify_ShortIDNamespaced(t *testing.T) {
	r := New()
	r.Module("core").RegisterWorkflow("login", noopWorkflow)
	if _, ok := r.workflows["core:login"]; !ok {
		t.Fatal(`expected workflow registered as "core:login"`)
	}
}

func TestQualify_CrossModulePrefixRejected(t *testing.T) {
	r := New()
	r.Module("gitops").RegisterWorkflow("pipeline:foo", noopWorkflow)
	if _, ok := r.workflows["pipeline:foo"]; ok {
		t.Fatal("cross-module workflow must not be registered")
	}
	if len(r.initErrs) == 0 {
		t.Fatal("expected init error for cross-module prefix")
	}
	if !strings.Contains(r.initErrs[0], `has prefix "pipeline" but module is "gitops"`) {
		t.Fatalf("init error %q missing expected substring", r.initErrs[0])
	}
}

func TestQualify_CorePrefixRegistrationRejected(t *testing.T) {
	r := New()
	r.Module("core").RegisterWorkflow("core:login", noopWorkflow)
	if _, ok := r.workflows["core:login"]; ok {
		t.Fatal("module must not register with reserved core: prefix")
	}
	if len(r.initErrs) == 0 {
		t.Fatal("expected init error for core: registration")
	}
	if !strings.Contains(r.initErrs[0], `cannot register "core:login" with reserved prefix "core"`) {
		t.Fatalf("init error %q missing expected substring", r.initErrs[0])
	}
}

func TestQualify_CorePrefixSpecPassthrough(t *testing.T) {
	r := New()
	r.RegisterWorkflow("core:shared", noopWorkflow)
	cs := &spec.CommandSpec{
		Command:     "execute shared",
		Verb:        VerbExecute,
		Noun:        "shared",
		Module:      "pipeline",
		HandlerType: spec.HandlerWorkflow,
		WorkflowID:  "core:shared",
	}
	if err := r.Module("pipeline").Register(cs); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if cs.WorkflowID != "core:shared" {
		t.Fatalf("WorkflowID = %q, want %q", cs.WorkflowID, "core:shared")
	}
	if err := r.CheckFunctions(); err != nil {
		t.Fatalf("CheckFunctions() = %v, want nil", err)
	}
}

func TestQualify_MultipleColonsRejected(t *testing.T) {
	r := New()
	r.Module("gitops").RegisterWorkflow("gitops:foo:bar", noopWorkflow)
	if _, ok := r.workflows["gitops:foo:bar"]; ok {
		t.Fatal("workflow with multiple colons must not be registered")
	}
	if len(r.initErrs) == 0 {
		t.Fatal("expected init error for multiple colons")
	}
	if !strings.Contains(r.initErrs[0], "cannot contain more than one colon") {
		t.Fatalf("init error %q missing expected substring", r.initErrs[0])
	}
}
