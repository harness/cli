// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package format

// ExecutionStatusBucket is the broad category an execution status maps to.
type ExecutionStatusBucket int

const (
	StatusSuccess ExecutionStatusBucket = iota
	StatusSkipped
	StatusFailed
	StatusRunning
	StatusWaiting
	StatusNoData
)

// ClassifyExecutionStatus maps a Harness execution status string (PascalCase or
// legacy SCREAMING_SNAKE_CASE) to one of the five display buckets. Also accepts
// the lowercase Harness Code check-status vocabulary (pending, running, success,
// failure, failure_ignored, error).
func ClassifyExecutionStatus(status string) ExecutionStatusBucket {
	switch status {
	case "Success", "IgnoreFailed",
		"IGNORE_FAILED",
		"success", "failure_ignored":
		return StatusSuccess
	case "Skipped", "SKIPPED":
		return StatusSkipped
	case "Failed", "Errored", "Aborted", "AbortedByFreeze", "Expired",
		"ApprovalRejected", "RollbackFailed",
		"APPROVAL_REJECTED", "ROLLBACK_FAILED",
		"failure", "error":
		return StatusFailed
	case "Running", "Pausing", "Discontinuing",
		"AsyncWaiting", "TaskWaiting", "TimedWaiting", "WaitStepRunning", "UploadWaiting",
		"running":
		return StatusRunning
	case "ResourceWaiting",
		"InterventionWaiting", "ApprovalWaiting",
		"InputWaiting", "Waiting",
		"Paused", "Queued", "QueuedLicenseLimitReached",
		"QueuedExecutionConcurrencyReached", "QueuedGlobalInfraCapacityReached",
		"Suspended", "NotStarted",
		"ASYNC_WAITING", "TASK_WAITING", "TIMED_WAITING",
		"INTERVENTION_WAITING", "APPROVAL_WAITING", "NOT_STARTED",
		"pending":
		return StatusWaiting
	default:
		return StatusNoData
	}
}

// BucketStyle holds all display properties for a status bucket.
type BucketStyle struct {
	NodeGlyph      string // ✓ ✗ ▶ ⊘ ○  — execution tree / status icons
	SparklinePty   string // pre-built ANSI-colored glyph for PTY sparklines
	SparklinePlain string // plain ASCII for non-PTY sparklines
	AnsiCode       int    // ANSI 16-color code; cast to console.Color for console.WithColor
	LipglossColor  string // 256-color code for lipgloss.Color()
}

// BucketStyles holds display properties for each execution status bucket.
var BucketStyles = map[ExecutionStatusBucket]BucketStyle{
	StatusSuccess: {"✓", "\x1b[32m▪\x1b[0m", "+", 32, "82"},
	StatusSkipped: {"↷", "\x1b[90m▪\x1b[0m", "-", 90, "243"},
	StatusFailed:  {"✗", "\x1b[31m▪\x1b[0m", "x", 31, "196"},
	StatusRunning: {"▶", "\x1b[34m▸\x1b[0m", ">", 36, "39"},
	StatusWaiting: {"⊘", "\x1b[33m◌\x1b[0m", "o", 33, "220"},
	StatusNoData:  {"○", "\x1b[90m·\x1b[0m", ".", 90, "240"},
}
