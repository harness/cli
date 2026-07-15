// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package telemetry

import "os"

// AI agent identifiers. These are the values sent in the "aiagent" telemetry
// property — lowercase, hyphenated, stable across agent versions.
const (
	AgentClaudeCode = "claude-code"
	AgentCursor     = "cursor"
	AgentGeminiCLI  = "gemini-cli"
	AgentCodexCLI   = "codex-cli"
	AgentAugment    = "augment"
	AgentCline      = "cline"
	AgentOpenCode   = "opencode"
	AgentTrae       = "trae"
	AgentDevin      = "devin"
)

// DetectAgent returns a standardized, lowercase identifier for the coding
// agent the CLI appears to be running under, or "" if none is detected.
// Detection relies on env vars each agent sets in its own subprocesses.
func DetectAgent() string {
	switch {
	case os.Getenv("CLAUDECODE") == "1":
		return AgentClaudeCode
	case os.Getenv("CURSOR_AGENT") == "1":
		return AgentCursor
	case os.Getenv("GEMINI_CLI") == "1":
		return AgentGeminiCLI
	case os.Getenv("CODEX_SANDBOX") != "":
		return AgentCodexCLI
	case os.Getenv("AUGMENT_AGENT") == "1":
		return AgentAugment
	case os.Getenv("CLINE_ACTIVE") == "true":
		return AgentCline
	case os.Getenv("OPENCODE_CLIENT") == "1":
		return AgentOpenCode
	case os.Getenv("TRAE_AI_SHELL_ID") != "":
		return AgentTrae
	case devinDetected():
		return AgentDevin
	default:
		return ""
	}
}

// devinDetected checks for the filesystem marker Devin sandboxes carry.
func devinDetected() bool {
	_, err := os.Stat("/opt/.devin")
	return err == nil
}
