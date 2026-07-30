// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var mvnHarURLPattern = regexp.MustCompile(`(?:https?://[^/]+)/(?:pkg/)?([^/]+)/([^/]+)/maven/?`)
var mvn403Pattern = regexp.MustCompile(`(?i)(403\s*Forbidden|status\s*code:\s*403|Return code is:\s*403|HTTP/\S+\s+403)`)

type mavenClient struct{}

func (c *mavenClient) Name() string { return "mvn" }

func (c *mavenClient) DetectRegistry(explicitRegistry string) (*pkgmgrRegistryInfo, error) {
	if saved := loadPkgmgrConfig("maven"); saved != nil {
		if explicitRegistry == "" || explicitRegistry == saved.RegistryIdentifier {
			return &pkgmgrRegistryInfo{
				RegistryURL:        saved.RegistryURL,
				RegistryIdentifier: saved.RegistryIdentifier,
				OrgID:              saved.OrgID,
				ProjectID:          saved.ProjectID,
			}, nil
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		info, err := mvnParseSettings(filepath.Join(home, ".m2", "settings.xml"), explicitRegistry)
		if err == nil && info != nil {
			return info, nil
		}
	}
	if explicitRegistry != "" {
		return nil, fmt.Errorf("HAR registry %q not found in Maven settings.xml", explicitRegistry)
	}
	return nil, fmt.Errorf("no HAR registry found — run `harness configure registry --client maven` first")
}

func (c *mavenClient) RunCommand(subcommand string, args []string) (*pkgmgrInstallResult, error) {
	cmdArgs := append([]string{subcommand}, args...)
	cmd := exec.Command("mvn", cmdArgs...)
	cmd.Stdin = os.Stdin
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &stdoutBuf)
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderrBuf)
	err := cmd.Run()
	combined := stdoutBuf.String() + "\n" + stderrBuf.String()
	if err != nil {
		return &pkgmgrInstallResult{Status: "FAILURE", Stderr: combined, Err: err}, nil
	}
	return &pkgmgrInstallResult{Status: "SUCCESS", Stderr: combined}, nil
}

func (c *mavenClient) DetectFirewallError(stderr string) bool {
	return mvn403Pattern.MatchString(stderr)
}

func (c *mavenClient) ResolveDeps() ([]dependency, func(), error) {
	noop := func() {}

	cmd := exec.Command("mvn", "dependency:tree", "-DoutputType=text")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if _, statErr := os.Stat("pom.xml"); statErr == nil {
			deps, err := parseLockFile("pom.xml")
			return deps, noop, err
		}
		return nil, noop, fmt.Errorf("mvn dependency:tree failed and no pom.xml found: %w", err)
	}

	deps := mvnParseDependencyTree(stdout.String())
	if len(deps) == 0 {
		if _, statErr := os.Stat("pom.xml"); statErr == nil {
			deps, err := parseLockFile("pom.xml")
			return deps, noop, err
		}
		return nil, noop, fmt.Errorf("no dependencies resolved from mvn dependency:tree")
	}
	return deps, noop, nil
}

// mvnParseDependencyTree parses `mvn dependency:tree -DoutputType=text` output.
// Lines look like: [INFO] +- com.google.guava:guava:jar:31.1-jre:compile
func mvnParseDependencyTree(output string) []dependency {
	depRe := regexp.MustCompile(`[|+\\\- ]+\s*(\S+):(\S+):(\S+):(\S+):(\S+)`)
	seen := map[string]bool{}
	var deps []dependency
	for _, line := range strings.Split(output, "\n") {
		if !strings.Contains(line, "[INFO]") {
			continue
		}
		m := depRe.FindStringSubmatch(strings.TrimPrefix(line, "[INFO] "))
		if len(m) < 6 {
			continue
		}
		name := m[1] + ":" + m[2]
		version := m[4]
		key := name + "@" + version
		if seen[key] {
			continue
		}
		seen[key] = true
		deps = append(deps, dependency{Name: name, Version: version})
	}
	return deps
}

func mvnParseSettings(settingsPath, explicitRegistry string) (*pkgmgrRegistryInfo, error) {
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return nil, err
	}
	type Mirror struct {
		ID  string `xml:"id"`
		URL string `xml:"url"`
	}
	type Settings struct {
		Mirrors struct {
			Mirror []Mirror `xml:"mirror"`
		} `xml:"mirrors"`
	}
	var settings Settings
	if err := xml.Unmarshal(data, &settings); err != nil {
		return nil, err
	}
	for _, m := range settings.Mirrors.Mirror {
		if !mvnHarURLPattern.MatchString(m.URL) {
			continue
		}
		matches := mvnHarURLPattern.FindStringSubmatch(m.URL)
		if len(matches) < 3 {
			continue
		}
		regID := matches[2]
		if explicitRegistry != "" && regID != explicitRegistry {
			continue
		}
		return &pkgmgrRegistryInfo{RegistryURL: m.URL, RegistryIdentifier: regID}, nil
	}
	return nil, fmt.Errorf("no HAR registry URL found in %s", settingsPath)
}
