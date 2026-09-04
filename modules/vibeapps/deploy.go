// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package vibeapps

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/harness/cli/pkg/client"
	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/console"
	"github.com/harness/cli/pkg/hlog"
)

const vibeappDeployWorkflowID = "vibeapp_deploy"

const vibeappLinkFile = ".harness/vibeapp.yaml"

// vibeappLink mirrors the .harness/vibeapp.yaml link file written by
// "execute vibeapp:deploy" and "pull vibeapp" to remember which Vibe App a
// local directory is linked to.
type vibeappLink struct {
	AppID string `yaml:"app_id"`
}

// loadVibeappLink reads root's link file. A nil, nil return means no link file exists.
func loadVibeappLink(root string) (*vibeappLink, error) {
	data, err := os.ReadFile(filepath.Join(root, vibeappLinkFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", vibeappLinkFile, err)
	}
	var link vibeappLink
	if err := yaml.Unmarshal(data, &link); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", vibeappLinkFile, err)
	}
	return &link, nil
}

// writeVibeappLink writes root's link file and best-effort ensures .harness/ is
// gitignored. Gitignore bookkeeping failures are only warned about, not fatal —
// the link file itself is what matters for future deploys.
func writeVibeappLink(root, appID string) error {
	dir := filepath.Join(root, ".harness")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating .harness directory: %w", err)
	}
	data, err := yaml.Marshal(&vibeappLink{AppID: appID})
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "vibeapp.yaml"), data, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", vibeappLinkFile, err)
	}
	hlog.Debug("wrote vibeapp link file", "path", filepath.Join(root, vibeappLinkFile), "app_id", appID)
	if err := ensureHarnessGitignored(root); err != nil {
		fmt.Fprintf(os.Stderr, "warning: %v\n", err)
	}
	return nil
}

// ensureHarnessGitignored appends ".harness/" to root's .gitignore (creating it if
// missing) unless an existing rule already covers it, per `git check-ignore`.
func ensureHarnessGitignored(root string) error {
	if gitCheckIgnore(root, ".harness") {
		return nil
	}
	gitignorePath := filepath.Join(root, ".gitignore")
	existing, err := os.ReadFile(gitignorePath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading .gitignore: %w", err)
	}
	content := string(existing)
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += ".harness/\n"
	if err := os.WriteFile(gitignorePath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("writing .gitignore: %w", err)
	}
	return nil
}

func gitCheckIgnore(root, target string) bool {
	cmd := exec.Command("git", "-C", root, "check-ignore", "-q", target)
	return cmd.Run() == nil
}

// gitProjectRoot resolves the git working tree root for the current directory, so
// "execute vibeapp:deploy" packages the whole project even when run from a subdirectory.
func gitProjectRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("not inside a git working tree (required for 'execute vibeapp:deploy'): %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// alwaysExcludedPaths are dropped from the deploy zip regardless of .gitignore
// contents (or absence): .git and .harness are CLI/VCS bookkeeping, not app source,
// and node_modules is a hard blacklist against oversized/misconfigured-gitignore zips.
var alwaysExcludedPaths = []string{".git/", ".harness/", "node_modules/"}

func isAlwaysExcluded(relPath string) bool {
	slashed := filepath.ToSlash(relPath)
	for _, prefix := range alwaysExcludedPaths {
		dir := strings.TrimSuffix(prefix, "/")
		if slashed == dir || strings.HasPrefix(slashed, prefix) {
			return true
		}
	}
	return false
}

// gitDeployFiles lists the files "execute vibeapp:deploy" should zip: tracked plus
// untracked-but-not-ignored files (so .gitignore, including nested/global excludes,
// is honored with no reimplementation of ignore logic), minus alwaysExcludedPaths.
func gitDeployFiles(root string) ([]string, error) {
	out, err := exec.Command("git", "-C", root, "ls-files", "-c", "-o", "--exclude-standard").Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	var files []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || isAlwaysExcluded(line) {
			continue
		}
		files = append(files, line)
	}
	return files, nil
}

// deployFileStats is the local, read-only "what would get zipped" summary printed
// before every deploy (including --check) so a human/agent can sanity-check the file
// count and size before anything is uploaded.
type deployFileStats struct {
	Files      []string
	TotalBytes int64
}

// computeDeployFileStats runs gitDeployFiles and stats each entry (rather than
// actually zipping) to report count/size cheaply and identically for --check and a
// real deploy.
func computeDeployFileStats(root string) (*deployFileStats, error) {
	files, err := gitDeployFiles(root)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no files to deploy: 'git ls-files' returned nothing under %s (everything gitignored?)", root)
	}
	var total int64
	for _, rel := range files {
		fi, err := os.Lstat(filepath.Join(root, rel))
		if err != nil {
			return nil, fmt.Errorf("stat %q: %w", rel, err)
		}
		if fi.Mode().IsRegular() {
			total += fi.Size()
		}
	}
	return &deployFileStats{Files: files, TotalBytes: total}, nil
}

// zipProjectDirFiles zips root's already-resolved deployable files (per
// computeDeployFileStats/gitDeployFiles) in memory.
func zipProjectDirFiles(root string, files []string) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, rel := range files {
		if err := addFileToZip(zw, root, rel); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("finalizing zip: %w", err)
	}
	return buf.Bytes(), nil
}

func addFileToZip(zw *zip.Writer, root, rel string) error {
	fullPath := filepath.Join(root, rel)
	fi, err := os.Lstat(fullPath)
	if err != nil {
		return fmt.Errorf("stat %q: %w", rel, err)
	}
	if !fi.Mode().IsRegular() {
		return nil
	}
	w, err := zw.Create(filepath.ToSlash(rel))
	if err != nil {
		return fmt.Errorf("creating zip entry %q: %w", rel, err)
	}
	f, err := os.Open(fullPath)
	if err != nil {
		return fmt.Errorf("opening %q: %w", rel, err)
	}
	defer f.Close()
	if _, err := io.Copy(w, f); err != nil {
		return fmt.Errorf("writing zip entry %q: %w", rel, err)
	}
	return nil
}

type vibeappSummary struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

// getVibeappByID fetches an app by id. A nil, nil return means the app doesn't exist.
func getVibeappByID(ctx *cmdctx.Ctx, id string) (*vibeappSummary, error) {
	raw, _, err := client.New(ctx).Get(apiPrefix+"/api/v1/apps/"+id, nil)
	if err != nil {
		if strings.Contains(err.Error(), "404") {
			return nil, nil
		}
		return nil, fmt.Errorf("fetching app %s: %w", id, err)
	}
	var app vibeappSummary
	if err := decodeInto(raw, &app); err != nil {
		return nil, fmt.Errorf("parsing app response: %w", err)
	}
	return &app, nil
}

func createVibeapp(ctx *cmdctx.Ctx, name, sourceID string) (*vibeappSummary, error) {
	path := fmt.Sprintf(apiPrefix+"/api/v1/spaces/%s/apps", sentinelSpaceID)
	raw, _, err := client.New(ctx).Post(path, nil, map[string]any{
		"name":     name,
		"sourceId": sourceID,
	})
	if err != nil {
		return nil, fmt.Errorf("creating app: %w", err)
	}
	var app vibeappSummary
	if err := decodeInto(raw, &app); err != nil {
		return nil, fmt.Errorf("parsing create-app response: %w", err)
	}
	if app.ID == "" {
		return nil, fmt.Errorf("create-app response had no app id")
	}
	return &app, nil
}

func triggerVibeappDeployment(ctx *cmdctx.Ctx, appID, workflowName string) (string, error) {
	body := map[string]any{}
	if workflowName != "" {
		body["workflowName"] = workflowName
	}
	raw, _, err := client.New(ctx).Post(apiPrefix+"/api/v1/apps/"+appID+"/deployments", nil, body)
	if err != nil {
		return "", fmt.Errorf("triggering deployment: %w", err)
	}
	deploymentID, _ := asMap(raw)["id"].(string)
	if deploymentID == "" {
		return "", fmt.Errorf("trigger-deployment response had no deployment id")
	}
	return deploymentID, nil
}

// deployPlanKind is the action "execute vibeapp:deploy" will take against the
// vibe-orchestrator, per the id-vs-link-file overwrite-semantics table.
type deployPlanKind string

const (
	deployPlanCreate       deployPlanKind = "create"        // no id, no (live) link: create a brand-new app
	deployPlanRecreateLink deployPlanKind = "recreate-link" // no id, link present but app is gone: create a new app, replacing the stale link
	deployPlanReuse        deployPlanKind = "reuse"         // linked app (matches explicit id if one was given): push a new version to it, no prompt
	deployPlanAdopt        deployPlanKind = "adopt"         // explicit id, no link file, app exists: prompt (unless --force), then link it
)

// deployPlan is the fully-resolved, side-effect-free description of what a deploy
// will do to the app side of things — computed identically for a real deploy and for
// --check, so --check can't drift from what actually happens.
type deployPlan struct {
	Kind    deployPlanKind
	AppID   string // set for Reuse/Adopt; empty for Create/RecreateLink
	AppName string // best-effort, from the GET; empty if not fetched (Create) or unknown
}

// resolveDeployPlan implements the id-vs-link-file overwrite-semantics table for
// "execute vibeapp:deploy" as a pure read (only GETs, no writes/prompts), so it can
// back both --check and the real deploy path. It errors exactly when a real deploy
// would hard-error: an explicitly-passed id that doesn't exist, or one that conflicts
// with an existing link file.
func resolveDeployPlan(ctx *cmdctx.Ctx, explicitID string, link *vibeappLink) (*deployPlan, error) {
	switch {
	case explicitID != "" && link == nil:
		app, err := getVibeappByID(ctx, explicitID)
		if err != nil {
			return nil, err
		}
		if app == nil {
			return nil, fmt.Errorf("vibe app %s not found", explicitID)
		}
		return &deployPlan{Kind: deployPlanAdopt, AppID: app.ID, AppName: app.Name}, nil

	case explicitID != "" && link != nil:
		if explicitID != link.AppID {
			return nil, fmt.Errorf("this directory is linked to Vibe App %s (%s), which differs from the id passed (%s); remove %s if you meant to switch apps", link.AppID, vibeappLinkFile, explicitID, vibeappLinkFile)
		}
		return &deployPlan{Kind: deployPlanReuse, AppID: explicitID}, nil

	case explicitID == "" && link != nil:
		app, err := getVibeappByID(ctx, link.AppID)
		if err != nil {
			return nil, err
		}
		if app == nil {
			// The linked app is gone server-side, but the link file is still the
			// strongest signal this directory owns that app slot: create a new app
			// rather than erroring, and the caller overwrites the link with its id.
			return &deployPlan{Kind: deployPlanRecreateLink}, nil
		}
		return &deployPlan{Kind: deployPlanReuse, AppID: app.ID, AppName: app.Name}, nil

	default: // explicitID == "" && link == nil
		return &deployPlan{Kind: deployPlanCreate}, nil
	}
}

// describeDeployPlan renders plan as the human-readable line printed for both --check
// and a real deploy, before anything happens.
func describeDeployPlan(plan *deployPlan, link *vibeappLink) string {
	switch plan.Kind {
	case deployPlanCreate:
		return "Plan: no linked app found — will create a new Vibe App"
	case deployPlanRecreateLink:
		return fmt.Sprintf("Plan: %s points at Vibe App %s, which no longer exists — will create a new Vibe App and replace the link", vibeappLinkFile, link.AppID)
	case deployPlanReuse:
		return fmt.Sprintf("Plan: will push a new source version to linked Vibe App %s (%s) and trigger a run", plan.AppID, plan.AppName)
	case deployPlanAdopt:
		return fmt.Sprintf("Plan: Vibe App %s (%s) already exists — will prompt to adopt it, link this directory to it, push a new source version, and trigger a run", plan.AppID, plan.AppName)
	default:
		return "Plan: unknown"
	}
}

// vibeappDeployWorkflow implements "execute vibeapp:deploy [<id>] [--force] [--no-follow]
// [--check] [--workflow <name>]": zips the current git working tree, pushes it as a new
// source, creates or updates the linked Vibe App, and triggers a run.
func vibeappDeployWorkflow(ctx *cmdctx.Ctx) error {
	if err := requireGit(); err != nil {
		return err
	}
	root, err := gitProjectRoot()
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Project root: %s\n", root)
	if _, err := os.Stat(filepath.Join(root, ".gitignore")); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "warning: no .gitignore found at %s — everything under the project root not already tracked by git will be zipped and uploaded\n", root)
	}

	check := cmdctx.GetBool(ctx.FlagValues, "check")
	force := cmdctx.GetBool(ctx.FlagValues, "force")
	noFollow := cmdctx.GetBool(ctx.FlagValues, "no-follow")
	workflowName := cmdctx.GetString(ctx.FlagValues, "workflow")

	link, err := loadVibeappLink(root)
	if err != nil {
		return err
	}

	stats, err := computeDeployFileStats(root)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Files to deploy: %d (%s)\n", len(stats.Files), formatBytes(stats.TotalBytes))

	plan, err := resolveDeployPlan(ctx, ctx.Id, link)
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, describeDeployPlan(plan, link))

	if check {
		return nil
	}

	if plan.Kind == deployPlanAdopt && !force {
		question := fmt.Sprintf("Vibe App %s (%s) already exists — deploying will push a new version and trigger a run. Continue?", plan.AppID, plan.AppName)
		if !console.PromptYesNo(question) {
			return fmt.Errorf("canceled")
		}
	}

	zipData, err := zipProjectDirFiles(root, stats.Files)
	if err != nil {
		return err
	}
	tmpPath, err := writeTempZip(zipData)
	if err != nil {
		return err
	}
	defer os.Remove(tmpPath)

	name := filepath.Base(root)
	appID := plan.AppID

	if plan.Kind == deployPlanCreate || plan.Kind == deployPlanRecreateLink {
		fmt.Fprintln(os.Stderr, "Creating a new Vibe App...")
		status, err := createAndUploadSource(ctx, tmpPath, name, "", "")
		if err != nil {
			return err
		}
		app, err := createVibeapp(ctx, name, status.ID)
		if err != nil {
			return err
		}
		appID = app.ID
		fmt.Fprintf(os.Stderr, "Created Vibe App %s (%s)\n", app.Name, app.ID)
		if err := writeVibeappLink(root, appID); err != nil {
			return err
		}
	} else {
		if plan.Kind == deployPlanAdopt {
			if err := writeVibeappLink(root, appID); err != nil {
				return err
			}
		}
		fmt.Fprintf(os.Stderr, "Deploying to Vibe App %s\n", appID)
		if _, err := createAndUploadSource(ctx, tmpPath, name, appID, ""); err != nil {
			return err
		}
	}

	if uiURL := vibeappUIURL(ctx, appID); uiURL != "" {
		fmt.Fprintf(os.Stderr, "View in Launchpad: %s\n", uiURL)
	}

	deploymentID, err := triggerVibeappDeployment(ctx, appID, workflowName)
	if err != nil {
		return err
	}
	fmt.Printf("\nDeployment triggered for Vibe App %s (%s)\n", appID, deploymentID)

	if noFollow {
		fmt.Printf("\nTo follow:\nharness get vibeapp_deployment %s/%s\nharness get vibeapp_deployment:log %s/%s --follow\n", appID, deploymentID, appID, deploymentID)
		return nil
	}

	fmt.Fprintln(os.Stderr, "\nFollowing deployment log ...")
	view, err := streamDeploymentLog(ctx, deploymentID, true)
	if err != nil {
		return err
	}
	if view.Status == "failed" {
		return fmt.Errorf("deployment %s failed", deploymentID)
	}
	return nil
}

func writeTempZip(zipData []byte) (string, error) {
	tmp, err := os.CreateTemp("", "vibeapp-deploy-*.zip")
	if err != nil {
		return "", fmt.Errorf("creating temp zip: %w", err)
	}
	defer tmp.Close()
	if _, err := tmp.Write(zipData); err != nil {
		return "", fmt.Errorf("writing temp zip: %w", err)
	}
	return tmp.Name(), nil
}
