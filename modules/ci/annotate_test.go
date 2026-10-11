// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package ci

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/hbase"
)

func TestAnnotateNewEntry(t *testing.T) {
	env, result := annotate(annotationsEnvelope{}, annotationInput{
		ContextID: "build-status",
		Style:     "info",
		Summary:   "Build completed",
		Mode:      "replace",
		Priority:  3,
		StepID:    "step1",
	})

	if len(env.Annotations) != 1 {
		t.Fatalf("got %d annotations, want 1", len(env.Annotations))
	}
	entry := env.Annotations[0]
	if entry.ContextId != "build-status" || entry.Summary != "Build completed" || entry.Style != "info" || entry.Priority != 3 || entry.StepId != "step1" {
		t.Fatalf("unexpected entry: %+v", entry)
	}
	if result["context"] != "build-status" || result["stepid"] != "step1" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestAnnotateReplace(t *testing.T) {
	env := annotationsEnvelope{Annotations: []annotationEntry{
		{ContextId: "ctx", Style: "info", Summary: "old", Priority: 1},
	}}

	env, _ = annotate(env, annotationInput{
		ContextID: "ctx",
		Style:     "warning",
		Summary:   "new",
		Mode:      "replace",
		Priority:  5,
	})

	if len(env.Annotations) != 1 {
		t.Fatalf("got %d annotations, want 1", len(env.Annotations))
	}
	entry := env.Annotations[0]
	if entry.Summary != "new" || entry.Style != "warning" || entry.Priority != 5 || entry.Mode != "replace" {
		t.Fatalf("unexpected entry after replace: %+v", entry)
	}
}

func TestAnnotateReplaceKeepsStyleAndPriorityWhenOmitted(t *testing.T) {
	env := annotationsEnvelope{Annotations: []annotationEntry{
		{ContextId: "ctx", Style: "info", Summary: "old", Priority: 7},
	}}

	env, _ = annotate(env, annotationInput{
		ContextID: "ctx",
		Summary:   "new",
		Mode:      "replace",
	})

	entry := env.Annotations[0]
	if entry.Style != "info" || entry.Priority != 7 {
		t.Fatalf("replace with empty style/priority should keep old values, got %+v", entry)
	}
	if entry.Summary != "new" {
		t.Fatalf("replace must always overwrite summary, got %q", entry.Summary)
	}
}

func TestAnnotateAppend(t *testing.T) {
	env := annotationsEnvelope{Annotations: []annotationEntry{
		{ContextId: "ctx", Summary: "first"},
	}}

	env, _ = annotate(env, annotationInput{
		ContextID: "ctx",
		Summary:   "second",
		Mode:      "append",
	})

	got := env.Annotations[0].Summary
	if got != "first\nsecond" {
		t.Fatalf("got summary %q, want %q", got, "first\nsecond")
	}

	env, _ = annotate(env, annotationInput{ContextID: "ctx", Mode: "append"})
	if env.Annotations[0].Summary != "first\nsecond" {
		t.Fatalf("appending an empty summary must not change existing summary, got %q", env.Annotations[0].Summary)
	}
}

func TestAnnotateAppendOntoEmptySummary(t *testing.T) {
	env := annotationsEnvelope{Annotations: []annotationEntry{
		{ContextId: "ctx"},
	}}

	env, _ = annotate(env, annotationInput{ContextID: "ctx", Summary: "only", Mode: "append"})
	if env.Annotations[0].Summary != "only" {
		t.Fatalf("got %q, want %q (no leading newline)", env.Annotations[0].Summary, "only")
	}
}

func TestAnnotateDeleteTombstonesInPlace(t *testing.T) {
	env := annotationsEnvelope{Annotations: []annotationEntry{
		{ContextId: "ctx", Style: "error", Summary: "boom", Priority: 9, StepId: "old-step"},
	}}

	env, _ = annotate(env, annotationInput{ContextID: "ctx", Mode: "delete", StepID: "new-step"})

	if len(env.Annotations) != 1 {
		t.Fatalf("delete must tombstone, not remove: got %d entries", len(env.Annotations))
	}
	entry := env.Annotations[0]
	if entry.Mode != "delete" || entry.Summary != "" || entry.Style != "" || entry.Priority != 0 {
		t.Fatalf("unexpected tombstoned entry: %+v", entry)
	}
	if entry.StepId != "new-step" {
		t.Fatalf("delete should still refresh a non-empty step id, got %q", entry.StepId)
	}
}

func TestAnnotateRootFieldsFirstWriteWins(t *testing.T) {
	env := annotationsEnvelope{AccountId: "acct-existing"}

	env, _ = annotate(env, annotationInput{
		ContextID: "ctx",
		Mode:      "replace",
		AccountID: "acct-from-env",
		OrgID:     "org-from-env",
		ProjectID: "proj-from-env",
	})

	if env.AccountId != "acct-existing" {
		t.Fatalf("account id already set should not be overwritten, got %q", env.AccountId)
	}
	if env.OrgId != "org-from-env" || env.ProjectId != "proj-from-env" {
		t.Fatalf("empty root fields should be filled from input, got org=%q project=%q", env.OrgId, env.ProjectId)
	}

	env, _ = annotate(env, annotationInput{ContextID: "ctx2", Mode: "replace", OrgID: "org-second-write"})
	if env.OrgId != "org-from-env" {
		t.Fatalf("org id already set should not be overwritten on a later write, got %q", env.OrgId)
	}
}

func TestLoadSaveEnvelopeRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "annotations.json")

	env, err := loadEnvelope(path)
	if err != nil {
		t.Fatalf("loadEnvelope on missing file: %v", err)
	}
	if len(env.Annotations) != 0 {
		t.Fatalf("missing file should load an empty envelope, got %+v", env)
	}

	env.Annotations = append(env.Annotations, annotationEntry{ContextId: "ctx", Summary: "hi"})
	if err := saveEnvelope(path, env); err != nil {
		t.Fatalf("saveEnvelope: %v", err)
	}

	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("tmp file should be renamed away, stat err: %v", err)
	}

	got, err := loadEnvelope(path)
	if err != nil {
		t.Fatalf("loadEnvelope after save: %v", err)
	}
	if len(got.Annotations) != 1 || got.Annotations[0].ContextId != "ctx" {
		t.Fatalf("round-tripped envelope mismatch: %+v", got)
	}
}

func TestLoadEnvelopeEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "annotations.json")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	env, err := loadEnvelope(path)
	if err != nil {
		t.Fatalf("loadEnvelope on empty file: %v", err)
	}
	if len(env.Annotations) != 0 {
		t.Fatalf("empty file should load an empty envelope, got %+v", env)
	}
}

func TestLoadEnvelopeInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "annotations.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if _, err := loadEnvelope(path); err == nil {
		t.Fatal("expected an error for invalid JSON, got nil")
	}
}

func TestReadSummaryFileEmptyPath(t *testing.T) {
	got, err := readSummaryFile("")
	if err != nil || got != "" {
		t.Fatalf("got (%q, %v), want (\"\", nil)", got, err)
	}
}

func TestReadSummaryFileTooLarge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "summary.md")
	if err := os.WriteFile(path, make([]byte, maxSummaryFileBytes+1), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if _, err := readSummaryFile(path); err == nil {
		t.Fatal("expected an error for an oversized summary file, got nil")
	}
}

func TestReadSummaryFileOK(t *testing.T) {
	path := filepath.Join(t.TempDir(), "summary.md")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	got, err := readSummaryFile(path)
	if err != nil || got != "hello" {
		t.Fatalf("got (%q, %v), want (\"hello\", nil)", got, err)
	}
}

func TestTruncateUTF8ByBytes(t *testing.T) {
	if got := truncateUTF8ByBytes("short", 100); got != "short" {
		t.Fatalf("got %q, want unchanged %q", got, "short")
	}
	long := strings.Repeat("a", 10)
	if got := truncateUTF8ByBytes(long, 4); got != "aaaa" {
		t.Fatalf("got %q, want %q", got, "aaaa")
	}
}

func TestIsAnnotationsEnabled(t *testing.T) {
	t.Setenv(envEnableCI, "")
	t.Setenv(envEnablePipe, "")
	if isAnnotationsEnabled() {
		t.Fatal("expected disabled when neither env var is set")
	}

	t.Setenv(envEnableCI, "true")
	if !isAnnotationsEnabled() {
		t.Fatal("expected enabled when CI_ENABLE_HARNESS_ANNOTATIONS=true")
	}

	t.Setenv(envEnableCI, "")
	t.Setenv(envEnablePipe, "true")
	if !isAnnotationsEnabled() {
		t.Fatal("expected enabled when PIPE_HARNESS_ANNOTATIONS=true")
	}
}

func enableAnnotations(t *testing.T) string {
	t.Helper()
	t.Setenv(envEnableCI, "true")
	path := filepath.Join(t.TempDir(), "annotations.json")
	t.Setenv(envAnnotFile, path)
	return path
}

func baseFlags() map[string]any {
	return map[string]any{
		"context":      "build-status",
		"style":        "info",
		"summary":      "Build completed",
		"summary-file": "",
		"mode":         "replace",
		"priority":     "3",
	}
}

func TestExecuteAnnotationHandlerFeatureFlagOff(t *testing.T) {
	t.Setenv(envEnableCI, "")
	t.Setenv(envEnablePipe, "")
	path := filepath.Join(t.TempDir(), "annotations.json")
	t.Setenv(envAnnotFile, path)

	err := executeAnnotationHandler(&cmdctx.Ctx{FlagValues: baseFlags()})
	if err != nil {
		t.Fatalf("expected a silent no-op, got error: %v", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("expected no file to be written while the feature flag is off, stat err: %v", statErr)
	}
}

func TestExecuteAnnotationHandlerRejectsStrayArgs(t *testing.T) {
	enableAnnotations(t)
	err := executeAnnotationHandler(&cmdctx.Ctx{FlagValues: baseFlags(), Args: []string{"extra"}})
	if err == nil || !strings.Contains(err.Error(), "unexpected arguments") {
		t.Fatalf("got %v, want an unexpected-arguments error", err)
	}
}

func TestExecuteAnnotationHandlerValidation(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(map[string]any)
		wantErr string
	}{
		{"bad style", func(f map[string]any) { f["style"] = "critical" }, "--style must be one of"},
		{"bad mode", func(f map[string]any) { f["mode"] = "merge" }, "--mode must be one of"},
		{"priority too low", func(f map[string]any) { f["priority"] = "0" }, "--priority must be between"},
		{"priority too high", func(f map[string]any) { f["priority"] = "11" }, "--priority must be between"},
		{"empty context", func(f map[string]any) { f["context"] = "   " }, "--context is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enableAnnotations(t)
			flags := baseFlags()
			tt.mutate(flags)
			err := executeAnnotationHandler(&cmdctx.Ctx{FlagValues: flags})
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("got %v, want an error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestExecuteAnnotationHandlerWritesFile(t *testing.T) {
	path := enableAnnotations(t)
	t.Setenv(hbase.EnvAccountID, "acct1")
	t.Setenv(hbase.EnvOrgID, "org1")
	t.Setenv(hbase.EnvProjectID, "proj1")
	t.Setenv(envStepID, "step1")

	if err := executeAnnotationHandler(&cmdctx.Ctx{FlagValues: baseFlags()}); err != nil {
		t.Fatalf("executeAnnotationHandler: %v", err)
	}

	env, err := loadEnvelope(path)
	if err != nil {
		t.Fatalf("loadEnvelope: %v", err)
	}
	if env.AccountId != "acct1" || env.OrgId != "org1" || env.ProjectId != "proj1" {
		t.Fatalf("unexpected root fields: %+v", env)
	}
	if len(env.Annotations) != 1 {
		t.Fatalf("got %d annotations, want 1", len(env.Annotations))
	}
	entry := env.Annotations[0]
	if entry.ContextId != "build-status" || entry.Summary != "Build completed" || entry.StepId != "step1" {
		t.Fatalf("unexpected entry: %+v", entry)
	}
}

func TestExecuteAnnotationHandlerSummaryFileWinsOverSummary(t *testing.T) {
	path := enableAnnotations(t)
	summaryPath := filepath.Join(t.TempDir(), "summary.md")
	if err := os.WriteFile(summaryPath, []byte("from file"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	flags := baseFlags()
	flags["summary"] = "from inline"
	flags["summary-file"] = summaryPath

	if err := executeAnnotationHandler(&cmdctx.Ctx{FlagValues: flags}); err != nil {
		t.Fatalf("executeAnnotationHandler: %v", err)
	}

	env, err := loadEnvelope(path)
	if err != nil {
		t.Fatalf("loadEnvelope: %v", err)
	}
	if got := env.Annotations[0].Summary; got != "from file" {
		t.Fatalf("got summary %q, want %q (summary-file should win)", got, "from file")
	}
}
