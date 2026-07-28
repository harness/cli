// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var npmHarURLPattern = regexp.MustCompile(`(?:https?://[^/]+)/(?:pkg/)?([^/]+)/([^/]+)/npm/?$`)
var npm403Pattern = regexp.MustCompile(`(?i)(403\s*forbidden|E403|\bstatus\s+403\b)`)

type npmClient struct{}

func (c *npmClient) Name() string { return "npm" }

func (c *npmClient) DetectRegistry(explicitRegistry string) (*pkgmgrRegistryInfo, error) {
	if saved := loadPkgmgrConfig("npm"); saved != nil {
		if explicitRegistry == "" || explicitRegistry == saved.RegistryIdentifier {
			return &pkgmgrRegistryInfo{
				RegistryURL:        saved.RegistryURL,
				RegistryIdentifier: saved.RegistryIdentifier,
				OrgID:              saved.OrgID,
				ProjectID:          saved.ProjectID,
			}, nil
		}
	}
	paths := []string{filepath.Join(".", ".npmrc")}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".npmrc"))
	}
	for _, p := range paths {
		info, err := npmParseNpmrc(p, explicitRegistry)
		if err == nil && info != nil {
			return info, nil
		}
	}
	if explicitRegistry != "" {
		return nil, fmt.Errorf("HAR registry %q not found in .npmrc", explicitRegistry)
	}
	return nil, fmt.Errorf("no HAR registry found — run `harness configure registry --client npm` first")
}

func (c *npmClient) RunCommand(subcommand string, args []string) (*pkgmgrInstallResult, error) {
	cmdArgs := append([]string{subcommand}, args...)
	cmd := exec.Command("npm", cmdArgs...)
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

func (c *npmClient) DetectFirewallError(stderr string) bool {
	return npm403Pattern.MatchString(stderr)
}

func (c *npmClient) ResolveDeps() ([]dependency, func(), error) {
	noop := func() {}
	lockFiles := []string{"package-lock.json", "yarn.lock", "pnpm-lock.yaml"}
	for _, lf := range lockFiles {
		if _, err := os.Stat(lf); err == nil {
			deps, err := parseLockFile(lf)
			if err == nil && len(deps) > 0 {
				return deps, noop, nil
			}
		}
	}

	// Generate lock file
	cmd := exec.Command("npm", "install", "--package-lock-only")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if _, statErr := os.Stat("package.json"); statErr == nil {
			deps, err := parseLockFile("package.json")
			return deps, noop, err
		}
		return nil, noop, fmt.Errorf("no dependency files found: %w", err)
	}
	cleanup := func() { os.Remove("package-lock.json") }
	deps, err := parseLockFile("package-lock.json")
	return deps, cleanup, err
}

func npmParseNpmrc(path, explicitRegistry string) (*pkgmgrRegistryInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var registryURL string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.Contains(line, "registry=") {
			parts := strings.SplitN(line, "registry=", 2)
			if len(parts) == 2 {
				candidate := strings.TrimSpace(parts[1])
				if npmHarURLPattern.MatchString(candidate) {
					registryURL = candidate
				}
			}
		}
	}
	if registryURL == "" {
		return nil, fmt.Errorf("no HAR registry URL in %s", path)
	}
	matches := npmHarURLPattern.FindStringSubmatch(registryURL)
	if len(matches) < 3 {
		return nil, fmt.Errorf("failed to parse HAR URL: %s", registryURL)
	}
	regID := matches[2]
	if explicitRegistry != "" && regID != explicitRegistry {
		return nil, fmt.Errorf("registry mismatch")
	}
	return &pkgmgrRegistryInfo{RegistryURL: registryURL, RegistryIdentifier: regID}, nil
}

