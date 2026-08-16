// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package vibe

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/harness/cli/pkg/auth"
	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/console"
)

type ideContext struct {
	TaskType        string      `json:"taskType"`
	ApplicationID   string      `json:"applicationId"`
	ApplicationName string      `json:"applicationName,omitempty"`
	DeploymentID    string      `json:"deploymentId"`
	ExecutionID     string      `json:"executionId"`
	Host            string      `json:"host"`
	Agent           string      `json:"agent"`
	Source          ideSource   `json:"source"`
	Failure         *ideFailure `json:"failure"`
	RequestedAction string      `json:"requestedAction"`
	APIBaseURL      string      `json:"apiBaseUrl,omitempty"`
}

type ideSource struct {
	Kind       string            `json:"kind"`
	Repository *ideGitRepository `json:"repository,omitempty"`
}

type ideGitRepository struct {
	URL        string `json:"url"`
	BaseBranch string `json:"baseBranch,omitempty"`
	Commit     string `json:"commit,omitempty"`
}

type ideFailure struct {
	Stage            string   `json:"stage"`
	Message          string   `json:"message"`
	File             string   `json:"file,omitempty"`
	Line             *int     `json:"line,omitempty"`
	LogLines         []string `json:"logLines"`
	AgentInstruction string   `json:"agentInstruction,omitempty"`
}

type launchRequest struct {
	AppID       string `json:"appId"`
	ExecutionID string `json:"executionId"`
	Host        string `json:"host"`
}

func vibeLaunchHandler(ctx *cmdctx.Ctx) error {
	listen := cmdctx.GetBool(ctx.FlagValues, "listen")
	resolved := ctx.Auth
	if resolved == nil {
		if loaded, err := auth.Load(""); err == nil && loaded != nil {
			resolved = loaded
		}
	}

	if listen {
		return runListenBridge(ctx, resolved)
	}

	appID := cmdctx.GetString(ctx.FlagValues, "app")
	execID := cmdctx.GetString(ctx.FlagValues, "execution")
	host := cmdctx.GetString(ctx.FlagValues, "host")
	fromProtocol := cmdctx.GetBool(ctx.FlagValues, "from-protocol")
	if host == "" {
		host = "cursor"
	}
	if host != "cursor" {
		return fmt.Errorf("unsupported host %q (this slice is Cursor only)", host)
	}
	if appID == "" || execID == "" {
		return fmt.Errorf("required: --app <id> --execution <id> (or --listen)")
	}
	return runLaunch(resolved, launchRequest{AppID: appID, ExecutionID: execID, Host: host}, fromProtocol)
}

func runLaunch(a *auth.ResolvedAuth, req launchRequest, confirm bool) error {
	api := newVibeAPI(a)
	path := fmt.Sprintf("/api/apps/%s/ide-context?executionId=%s", req.AppID, req.ExecutionID)
	var ide ideContext
	if err := api.getJSON(path, &ide); err != nil {
		return err
	}
	ide.APIBaseURL = api.baseURL
	if ide.Host == "" {
		ide.Host = req.Host
	}
	if ide.Agent == "" {
		ide.Agent = "cursor"
	}

	if confirm {
		name := ide.ApplicationName
		if name == "" {
			name = ide.ApplicationID
		}
		stage := "deployment"
		if ide.Failure != nil && ide.Failure.Stage != "" {
			stage = ide.Failure.Stage
		}
		question := fmt.Sprintf("Open %s (failed %s) in Cursor?", name, stage)
		if !promptYesDefault(question) {
			fmt.Println("Cancelled.")
			return nil
		}
	}

	dest, err := workspaceDir(ide.ApplicationID, ide.ExecutionID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}

	if err := materializeSource(api, ide, dest); err != nil {
		return err
	}
	if err := writeHandoffFiles(dest, api.baseURL, ide); err != nil {
		return err
	}

	warnIfExtensionMissing()
	fmt.Printf("Opening Cursor on %s\n", dest)
	return openCursor(dest)
}

func promptYesDefault(question string) bool {
	if !console.IsBothTTY() {
		return true
	}
	fmt.Fprintf(os.Stderr, "%s [Y/n] ", question)
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		ans := strings.TrimSpace(strings.ToLower(scanner.Text()))
		return ans == "" || ans == "y" || ans == "yes"
	}
	return true
}

func workspaceDir(appID, executionID string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "harness", "workspaces", "vibe", appID, executionID), nil
}

func warnIfExtensionMissing() {
	path, err := exec.LookPath("cursor")
	if err != nil {
		return
	}
	out, err := exec.Command(path, "--list-extensions").Output()
	if err != nil {
		return
	}
	list := strings.ToLower(string(out))
	if strings.Contains(list, "harness-vibe") || strings.Contains(list, "harness.vibe") {
		return
	}
	fmt.Println("Hint: Vibe Cursor extension not installed. From harness-ai/extensions/cursor:")
	fmt.Println("  npx @vscode/vsce package --no-dependencies")
	fmt.Println("  cursor --install-extension ./harness-vibe-*.vsix")
}

func openCursor(dir string) error {
	if path, err := exec.LookPath("cursor"); err == nil {
		cmd := exec.Command(path, "--new-window", dir)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Start()
	}
	if runtime.GOOS == "darwin" {
		cmd := exec.Command("open", "-na", "Cursor", "--args", dir)
		return cmd.Start()
	}
	return fmt.Errorf("cursor CLI not found; in Cursor use Command Palette → Install 'cursor' command in PATH")
}
