// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package annotations

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const maxSummaryFileBytes = 64 * 1024

const (
	envExecutionID = "HARNESS_EXECUTION_ID"
	envStageID     = "HARNESS_STAGE_ID"
	envStepID      = "HARNESS_STEP_ID"
	envAnnotFile   = "HARNESS_ANNOTATIONS_FILE"
	envEnableCI    = "CI_ENABLE_HARNESS_ANNOTATIONS"
	envEnablePipe  = "PIPE_HARNESS_ANNOTATIONS"
)

type annotationEntry struct {
	ContextId string `json:"contextId"`
	StepId    string `json:"stepId,omitempty"`
	Timestamp int64  `json:"timestamp"`
	Style     string `json:"style"`
	Summary   string `json:"summary"`
	Priority  int    `json:"priority"`
	Mode      string `json:"mode,omitempty"`
}

type annotationsEnvelope struct {
	AccountId        string            `json:"accountId,omitempty"`
	OrgId            string            `json:"orgId,omitempty"`
	ProjectId        string            `json:"projectId,omitempty"`
	PipelineId       string            `json:"pipelineId,omitempty"`
	PlanExecutionId  string            `json:"planExecutionId,omitempty"`
	StageExecutionId string            `json:"stageExecutionId,omitempty"`
	Annotations      []annotationEntry `json:"annotations"`
}

// annotationInput holds the already-validated/normalized values a caller
// (the workflow handler) resolved from flags and env vars.
type annotationInput struct {
	ContextID        string
	Style            string
	Summary          string
	Mode             string
	Priority         int
	StepID           string
	AccountID        string
	OrgID            string
	ProjectID        string
	PipelineID       string
	PlanExecutionID  string
	StageExecutionID string
}

func isAnnotationsEnabled() bool {
	return os.Getenv(envEnableCI) == "true" || os.Getenv(envEnablePipe) == "true"
}

func truncateUTF8ByBytes(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit]
}

func loadEnvelope(path string) (annotationsEnvelope, error) {
	var env annotationsEnvelope
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return env, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return env, err
	}
	if len(data) == 0 {
		return env, nil
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return env, fmt.Errorf("invalid annotations file format: %w", err)
	}
	return env, nil
}

// saveEnvelope writes via a tmp-file-then-rename so a reader never observes a
// partially-written annotations file.
func saveEnvelope(path string, env annotationsEnvelope) error {
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("failed to create parent dir: %w", err)
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		// Windows can fail a rename onto an existing destination; retry once after removing it.
		_ = os.Remove(path)
		if err2 := os.Rename(tmp, path); err2 != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("failed to finalize write: %w", err2)
		}
	}
	return nil
}

func readSummaryFile(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("failed to stat summary file %q: %w", path, err)
	}
	if info.Size() > maxSummaryFileBytes {
		return "", fmt.Errorf("summary file %q exceeds %d bytes (64KB) with size %d bytes", path, maxSummaryFileBytes, info.Size())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read summary file %q: %w", path, err)
	}
	return string(data), nil
}

// annotate merges in into env: a new context id is appended as a new entry;
// an existing one is merged according to in.Mode (append/replace/delete).
// Root-level PMS identifiers are filled in only when currently empty, so the
// first write across a step/stage wins. in must already be validated and
// normalized (trimmed context, lowercased style/mode, clamped priority) —
// that is the handler's job, not this function's.
func annotate(env annotationsEnvelope, in annotationInput) (annotationsEnvelope, map[string]any) {
	if env.AccountId == "" && in.AccountID != "" {
		env.AccountId = in.AccountID
	}
	if env.OrgId == "" && in.OrgID != "" {
		env.OrgId = in.OrgID
	}
	if env.ProjectId == "" && in.ProjectID != "" {
		env.ProjectId = in.ProjectID
	}
	if env.PipelineId == "" && in.PipelineID != "" {
		env.PipelineId = in.PipelineID
	}
	if env.PlanExecutionId == "" && in.PlanExecutionID != "" {
		env.PlanExecutionId = in.PlanExecutionID
	}
	if env.StageExecutionId == "" && in.StageExecutionID != "" {
		env.StageExecutionId = in.StageExecutionID
	}

	idx := -1
	for i := range env.Annotations {
		if env.Annotations[i].ContextId == in.ContextID {
			idx = i
			break
		}
	}

	ts := time.Now().UnixMilli()
	if idx == -1 {
		env.Annotations = append(env.Annotations, annotationEntry{
			ContextId: in.ContextID,
			Timestamp: ts,
			Style:     in.Style,
			Summary:   in.Summary,
			Priority:  in.Priority,
			Mode:      in.Mode,
			StepId:    in.StepID,
		})
	} else {
		entry := env.Annotations[idx]
		entry.Timestamp = ts
		switch in.Mode {
		case "delete":
			entry.Mode = "delete"
			entry.Summary = ""
			entry.Style = ""
			entry.Priority = 0
			if in.StepID != "" {
				entry.StepId = in.StepID
			}
		case "replace":
			if in.Style != "" {
				entry.Style = in.Style
			}
			entry.Summary = in.Summary
			entry.Mode = "replace"
			if in.Priority > 0 {
				entry.Priority = in.Priority
			}
			if in.StepID != "" {
				entry.StepId = in.StepID
			}
		case "append":
			if in.Style != "" {
				entry.Style = in.Style
			}
			if in.Summary != "" {
				if entry.Summary != "" {
					entry.Summary += "\n" + in.Summary
				} else {
					entry.Summary = in.Summary
				}
			}
			entry.Mode = "append"
			if in.Priority > 0 {
				entry.Priority = in.Priority
			}
			if in.StepID != "" {
				entry.StepId = in.StepID
			}
		}
		env.Annotations[idx] = entry
	}

	result := map[string]any{
		"context": in.ContextID,
		"stepid":  in.StepID,
		"message": fmt.Sprintf("Annotation stored for context '%s' with step ID '%s'", in.ContextID, in.StepID),
	}
	return env, result
}
