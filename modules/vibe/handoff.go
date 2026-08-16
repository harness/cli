// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package vibe

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func writeHandoffFiles(dest, apiBase string, ide ideContext) error {
	if err := os.MkdirAll(filepath.Join(dest, ".harness"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dest, ".cursor"), 0o755); err != nil {
		return err
	}
	ide.APIBaseURL = apiBase
	raw, err := json.MarshalIndent(ide, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dest, ".harness", "vibe-context.json"), raw, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dest, "VIBE_TASK.md"), []byte(renderVibeTask(ide)), 0o644); err != nil {
		return err
	}
	mcp := map[string]any{"mcpServers": map[string]any{}}
	if cmd, args := resolveMcpEntry(); cmd != "" {
		env := map[string]string{"VIBE_API_BASE_URL": apiBase}
		mcp["mcpServers"] = map[string]any{
			"harness-vibe": map[string]any{
				"command": cmd,
				"args":    args,
				"env":     env,
			},
		}
	}
	mcpRaw, err := json.MarshalIndent(mcp, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dest, ".cursor", "mcp.json"), mcpRaw, 0o644)
}

func renderVibeTask(ide ideContext) string {
	name := ide.ApplicationName
	if name == "" {
		name = ide.ApplicationID
	}
	stage := "unknown"
	fileLine := "(see logs)"
	errMsg := ide.RequestedAction
	instruction := "Diagnose the failure, make the smallest fix, then redeploy."
	if ide.Failure != nil {
		stage = ide.Failure.Stage
		errMsg = ide.Failure.Message
		if ide.Failure.File != "" {
			fileLine = ide.Failure.File
			if ide.Failure.Line != nil {
				fileLine = fmt.Sprintf("%s:%d", ide.Failure.File, *ide.Failure.Line)
			}
		}
		if ide.Failure.AgentInstruction != "" {
			instruction = ide.Failure.AgentInstruction
		}
	}
	return fmt.Sprintf(`Fix Harness Vibe deployment %s.

Application: %s
Failure stage: %s
Relevant source: %s
Error: %s

Please:
1. Inspect the existing project.
2. Determine the root cause.
3. Make the smallest appropriate fix.
4. Validate locally where possible.
5. Redeploy using Harness Vibe MCP (deploy_vibe_app).
6. Confirm deployment success.

Agent instruction: %s

Prefer Vibe MCP tools (get_vibe_deployment, get_vibe_deployment_logs, deploy_vibe_app)
over raw curl. If VIBE_TASK.md exists, this file is the current task.
`, ide.DeploymentID, name, strings.ToUpper(stage), fileLine, errMsg, instruction)
}

func resolveMcpEntry() (command string, args []string) {
	if entry := os.Getenv("VIBE_MCP_ENTRY"); entry != "" {
		return mcpCommandFor(entry)
	}
	var candidates []string
	if root := os.Getenv("VIBE_MODE_ROOT"); root != "" {
		candidates = append(candidates,
			filepath.Join(root, "apps/vibe-mcp/dist/index.js"),
			filepath.Join(root, "apps/vibe-mcp/src/index.ts"),
		)
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, "trc/harness/vibe-mode/apps/vibe-mcp/dist/index.js"),
			filepath.Join(home, "trc/harness/vibe-mode/apps/vibe-mcp/src/index.ts"),
		)
	}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates,
			filepath.Join(cwd, "apps/vibe-mcp/dist/index.js"),
			filepath.Join(cwd, "apps/vibe-mcp/src/index.ts"),
			filepath.Join(cwd, "..", "vibe-mode", "apps/vibe-mcp/dist/index.js"),
			filepath.Join(cwd, "..", "vibe-mode", "apps/vibe-mcp/src/index.ts"),
		)
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return mcpCommandFor(c)
		}
	}
	return "", nil
}

func mcpCommandFor(entry string) (string, []string) {
	if strings.HasSuffix(entry, ".ts") {
		return "npx", []string{"--yes", "tsx", entry}
	}
	return "node", []string{entry}
}
