// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var nugetHarURLPattern = regexp.MustCompile(`(?:https?://[^/]+)/(?:pkg/)?([^/]+)/([^/]+)/nuget/?`)
var nuget403Pattern = regexp.MustCompile(`(?i)(403\s*\(Forbidden\)|Response status code does not indicate success:\s*403|HTTP\s+403)`)

type nugetClient struct{}

func (c *nugetClient) Name() string { return "dotnet" }

func (c *nugetClient) DetectRegistry(explicitRegistry string) (*pkgmgrRegistryInfo, error) {
	if saved := loadPkgmgrConfig("nuget"); saved != nil {
		if explicitRegistry == "" || explicitRegistry == saved.RegistryIdentifier {
			return &pkgmgrRegistryInfo{
				RegistryURL:        saved.RegistryURL,
				RegistryIdentifier: saved.RegistryIdentifier,
				OrgID:              saved.OrgID,
				ProjectID:          saved.ProjectID,
			}, nil
		}
	}
	paths := nugetConfPaths()
	for _, p := range paths {
		info, err := nugetParseConf(p, explicitRegistry)
		if err == nil && info != nil {
			return info, nil
		}
	}
	if explicitRegistry != "" {
		return nil, fmt.Errorf("HAR registry %q not found in NuGet configuration", explicitRegistry)
	}
	return nil, fmt.Errorf("no HAR registry found — run `harness configure registry --client nuget` first")
}

func (c *nugetClient) RunCommand(subcommand string, args []string) (*pkgmgrInstallResult, error) {
	cmdArgs := append([]string{subcommand}, args...)
	cmd := exec.Command("dotnet", cmdArgs...)
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

func (c *nugetClient) DetectFirewallError(stderr string) bool {
	return nuget403Pattern.MatchString(stderr)
}

func (c *nugetClient) ResolveDeps() ([]dependency, func(), error) {
	noop := func() {}

	// Try packages.lock.json or obj/project.assets.json first
	lockFiles := []string{"packages.lock.json"}
	if entries, err := filepath.Glob("**/obj/project.assets.json"); err == nil {
		lockFiles = append(lockFiles, entries...)
	}
	if _, err := os.Stat("obj/project.assets.json"); err == nil {
		lockFiles = append(lockFiles, "obj/project.assets.json")
	}
	for _, lf := range lockFiles {
		if _, err := os.Stat(lf); err == nil {
			deps, err := nugetParseLockFile(lf)
			if err == nil && len(deps) > 0 {
				return deps, noop, nil
			}
		}
	}

	// dotnet list package --include-transitive --format json
	cmd := exec.Command("dotnet", "list", "package", "--include-transitive", "--format", "json")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		deps, parseErr := nugetParseCsproj()
		if parseErr != nil || len(deps) == 0 {
			return nil, noop, fmt.Errorf("dotnet list package failed and no csproj deps found: %w", err)
		}
		return deps, noop, nil
	}

	deps, err := nugetParseDotnetListJSON(stdout.String())
	if err != nil || len(deps) == 0 {
		deps, _ = nugetParseCsproj()
	}
	return deps, noop, nil
}

func nugetParseLockFile(path string) ([]dependency, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(path, "packages.lock.json") {
		var lf struct {
			Dependencies map[string]map[string]struct {
				Resolved string `json:"resolved"`
			} `json:"dependencies"`
		}
		if err := json.Unmarshal(data, &lf); err != nil {
			return nil, err
		}
		seen := map[string]bool{}
		var deps []dependency
		for _, framework := range lf.Dependencies {
			for name, info := range framework {
				key := name + "@" + info.Resolved
				if seen[key] {
					continue
				}
				seen[key] = true
				deps = append(deps, dependency{Name: name, Version: info.Resolved})
			}
		}
		return deps, nil
	}
	// project.assets.json
	var assets struct {
		Libraries map[string]struct {
			Type string `json:"type"`
		} `json:"libraries"`
	}
	if err := json.Unmarshal(data, &assets); err != nil {
		return nil, err
	}
	var deps []dependency
	for lib, info := range assets.Libraries {
		if info.Type != "package" {
			continue
		}
		parts := strings.SplitN(lib, "/", 2)
		if len(parts) != 2 {
			continue
		}
		deps = append(deps, dependency{Name: parts[0], Version: parts[1]})
	}
	return deps, nil
}

func nugetParseDotnetListJSON(output string) ([]dependency, error) {
	var out struct {
		Projects []struct {
			Frameworks []struct {
				TopLevelPackages   []struct{ ID, Version string } `json:"topLevelPackages"`
				TransitivePackages []struct{ ID, Version string } `json:"transitivePackages"`
			} `json:"frameworks"`
		} `json:"projects"`
	}
	if err := json.Unmarshal([]byte(output), &out); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var deps []dependency
	for _, proj := range out.Projects {
		for _, fw := range proj.Frameworks {
			for _, pkg := range append(fw.TopLevelPackages, fw.TransitivePackages...) {
				key := pkg.ID + "@" + pkg.Version
				if seen[key] {
					continue
				}
				seen[key] = true
				deps = append(deps, dependency{Name: pkg.ID, Version: pkg.Version})
			}
		}
	}
	return deps, nil
}

func nugetParseCsproj() ([]dependency, error) {
	files, _ := filepath.Glob("*.csproj")
	if len(files) == 0 {
		_ = filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(path, ".csproj") {
				files = append(files, path)
			}
			return nil
		})
	}
	seen := map[string]bool{}
	var deps []dependency
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		type PackageRef struct {
			Include string `xml:"Include,attr"`
			Version string `xml:"Version,attr"`
		}
		type ItemGroup struct {
			PackageReferences []PackageRef `xml:"PackageReference"`
		}
		type Project struct {
			ItemGroups []ItemGroup `xml:"ItemGroup"`
		}
		var proj Project
		if err := xml.Unmarshal(data, &proj); err != nil {
			continue
		}
		for _, ig := range proj.ItemGroups {
			for _, ref := range ig.PackageReferences {
				if ref.Include == "" || seen[ref.Include] {
					continue
				}
				seen[ref.Include] = true
				ver := ref.Version
				if ver == "" {
					ver = "latest"
				}
				deps = append(deps, dependency{Name: ref.Include, Version: ver})
			}
		}
	}
	return deps, nil
}

func nugetConfPaths() []string {
	paths := []string{"nuget.config", "NuGet.Config", "NuGet.config"}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".nuget", "NuGet", "NuGet.Config"))
	}
	return paths
}

func nugetParseConf(confPath, explicitRegistry string) (*pkgmgrRegistryInfo, error) {
	data, err := os.ReadFile(confPath)
	if err != nil {
		return nil, err
	}
	type PackageSource struct {
		Key   string `xml:"key,attr"`
		Value string `xml:"value,attr"`
	}
	type Configuration struct {
		PackageSources struct {
			Sources []PackageSource `xml:"add"`
		} `xml:"packageSources"`
	}
	var conf Configuration
	if err := xml.Unmarshal(data, &conf); err != nil {
		return nil, err
	}
	for _, s := range conf.PackageSources.Sources {
		if !nugetHarURLPattern.MatchString(s.Value) {
			continue
		}
		matches := nugetHarURLPattern.FindStringSubmatch(s.Value)
		if len(matches) < 3 {
			continue
		}
		regID := matches[2]
		if explicitRegistry != "" && regID != explicitRegistry {
			continue
		}
		return &pkgmgrRegistryInfo{RegistryURL: s.Value, RegistryIdentifier: regID}, nil
	}
	return nil, fmt.Errorf("no HAR registry URL found in %s", confPath)
}
