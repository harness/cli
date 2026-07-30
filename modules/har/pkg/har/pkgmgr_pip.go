// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var pipHarURLPattern = regexp.MustCompile(`(?:https?://[^/@]+@?[^/]+)/(?:pkg/)?([^/]+)/([^/]+)/pypi/?`)
var pip403Pattern = regexp.MustCompile(`(?i)(403\s*Forbidden|HTTP\s+error\s+403|status\s*code\s*403|Client Error:\s*403)`)

type pipClient struct{}

func (c *pipClient) Name() string { return "pip" }

func (c *pipClient) DetectRegistry(explicitRegistry string) (*pkgmgrRegistryInfo, error) {
	if saved := loadPkgmgrConfig("pip"); saved != nil {
		if explicitRegistry == "" || explicitRegistry == saved.RegistryIdentifier {
			return &pkgmgrRegistryInfo{
				RegistryURL:        saved.RegistryURL,
				RegistryIdentifier: saved.RegistryIdentifier,
				OrgID:              saved.OrgID,
				ProjectID:          saved.ProjectID,
			}, nil
		}
	}
	paths := pipConfPaths()
	for _, p := range paths {
		info, err := pipParseConf(p, explicitRegistry)
		if err == nil && info != nil {
			return info, nil
		}
	}
	if explicitRegistry != "" {
		return nil, fmt.Errorf("HAR registry %q not found in pip configuration", explicitRegistry)
	}
	return nil, fmt.Errorf("no HAR registry found — run `harness configure registry --client pip` first")
}

func (c *pipClient) RunCommand(subcommand string, args []string) (*pkgmgrInstallResult, error) {
	bin := pipBinary()
	cmdArgs := append([]string{subcommand}, args...)
	cmd := exec.Command(bin, cmdArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	var stderrBuf bytes.Buffer
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderrBuf)
	err := cmd.Run()
	stderrStr := stderrBuf.String()
	if err != nil {
		return &pkgmgrInstallResult{Status: "FAILURE", Stderr: stderrStr, Err: err}, nil
	}
	return &pkgmgrInstallResult{Status: "SUCCESS", Stderr: stderrStr}, nil
}

func (c *pipClient) DetectFirewallError(stderr string) bool {
	return pip403Pattern.MatchString(stderr)
}

func (c *pipClient) ResolveDeps() ([]dependency, func(), error) {
	noop := func() {}
	for _, lf := range []string{"Pipfile.lock", "poetry.lock", "requirements.txt"} {
		if _, err := os.Stat(lf); err == nil {
			deps, err := parseLockFile(lf)
			if err == nil && len(deps) > 0 {
				return deps, noop, nil
			}
		}
	}

	// pip install --dry-run --report
	reportPath := filepath.Join(os.TempDir(), "pip-report.json")
	cleanup := func() { os.Remove(reportPath) }

	reqsArg, reqsFile := "-r", "requirements.txt"
	if _, err := os.Stat("requirements.txt"); err != nil {
		if _, err := os.Stat("pyproject.toml"); err == nil {
			reqsArg, reqsFile = ".", ""
		} else {
			return nil, noop, fmt.Errorf("no requirements.txt or pyproject.toml found")
		}
	}

	var cmdArgs []string
	if reqsFile != "" {
		cmdArgs = []string{"install", "--dry-run", "--report", reportPath, reqsArg, reqsFile}
	} else {
		cmdArgs = []string{"install", "--dry-run", "--report", reportPath, reqsArg}
	}

	cmd := exec.Command(pipBinary(), cmdArgs...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		cleanup()
		if _, statErr := os.Stat("requirements.txt"); statErr == nil {
			deps, err := parseLockFile("requirements.txt")
			return deps, noop, err
		}
		return nil, noop, fmt.Errorf("pip dry-run failed and no requirements.txt found: %w", err)
	}

	deps, err := pipParseDryRunReport(reportPath)
	if err != nil {
		cleanup()
		return nil, noop, err
	}
	return deps, cleanup, nil
}

func pipParseDryRunReport(reportPath string) ([]dependency, error) {
	data, err := os.ReadFile(reportPath)
	if err != nil {
		return nil, fmt.Errorf("reading pip report: %w", err)
	}
	var report struct {
		Install []struct {
			Metadata struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"metadata"`
		} `json:"install"`
	}
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("parsing pip report: %w", err)
	}
	deps := make([]dependency, 0, len(report.Install))
	for _, pkg := range report.Install {
		if pkg.Metadata.Name == "" {
			continue
		}
		deps = append(deps, dependency{Name: pkg.Metadata.Name, Version: pkg.Metadata.Version})
	}
	return deps, nil
}

func pipConfPaths() []string {
	var paths []string
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths,
			filepath.Join(home, ".config", "pip", "pip.conf"),
			filepath.Join(home, ".pip", "pip.conf"),
		)
	}
	paths = append(paths, "pip.conf")
	return paths
}

func pipParseConf(confPath, explicitRegistry string) (*pkgmgrRegistryInfo, error) {
	data, err := os.ReadFile(confPath)
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "index-url") && !strings.HasPrefix(line, "extra-index-url") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		rawURL := strings.TrimSpace(parts[1])
		if !pipHarURLPattern.MatchString(rawURL) {
			continue
		}
		matches := pipHarURLPattern.FindStringSubmatch(rawURL)
		if len(matches) < 3 {
			continue
		}
		regID := matches[2]
		if explicitRegistry != "" && regID != explicitRegistry {
			continue
		}
		return &pkgmgrRegistryInfo{RegistryURL: rawURL, RegistryIdentifier: regID}, nil
	}
	return nil, fmt.Errorf("no HAR registry URL found in %s", confPath)
}

func pipBinary() string {
	for _, b := range []string{"pip", "pip3"} {
		if _, err := exec.LookPath(b); err == nil {
			return b
		}
	}
	return "pip"
}
