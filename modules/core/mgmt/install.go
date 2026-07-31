// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package mgmt

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"golang.org/x/mod/semver"

	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/hbase"
	"github.com/harness/cli/pkg/hlog"
	"github.com/harness/cli/pkg/release"
)

var reReleaseVersion = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

// cmpVersion compares two version strings (with or without "v" prefix).
// Returns (-1|0|1, true) on valid semver input, or (0, false) if either is invalid.
func cmpVersion(a, b string) (int, bool) {
	av, bv := a, b
	if !strings.HasPrefix(av, "v") {
		av = "v" + av
	}
	if !strings.HasPrefix(bv, "v") {
		bv = "v" + bv
	}
	if !semver.IsValid(av) || !semver.IsValid(bv) {
		return 0, false
	}
	return semver.Compare(av, bv), true
}

// resolveVersion validates and normalizes a user-supplied version string,
// or fetches the latest release when version is "" or "latest".
func resolveVersion(version string) (string, error) {
	if version != "" && version != "latest" {
		if !strings.HasPrefix(version, "v") {
			version = "v" + version
		}
		if !reReleaseVersion.MatchString(version) {
			return "", fmt.Errorf("invalid version %q — expected vMAJOR.MINOR.PATCH (e.g. v1.2.3) or \"latest\"", version)
		}
		return version, nil
	}
	hlog.Debug("fetching latest release version")
	v, err := release.FetchLatestVersion()
	if err != nil {
		return "", fmt.Errorf("fetching latest version: %w", err)
	}
	hlog.Debug("latest release", "version", v)
	return v, nil
}

const (
	installBinaryName = "harness"
	installBundleName = "harness-core"
	installDefaultDir = "~/.local/bin"
)

func checkRunningFromInstallDir(installDir string) error {
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	exeDir, err1 := filepath.EvalSymlinks(filepath.Dir(exe))
	absInstall, err2 := filepath.EvalSymlinks(installDir)
	if err1 != nil || err2 != nil {
		return nil
	}
	if exeDir != absInstall {
		return fmt.Errorf(
			"harness is running from %s, not the install directory %s\n"+
				"Run the installed binary or pass --install-dir to point at %s",
			exeDir, absInstall, exeDir,
		)
	}
	return nil
}

func InstallCLIHandler(ctx *cmdctx.Ctx) error {
	version := cmdctx.GetString(ctx.FlagValues, "version")
	force := cmdctx.GetBool(ctx.FlagValues, "force")
	check := cmdctx.GetBool(ctx.FlagValues, "check")
	coreOnly := cmdctx.GetBool(ctx.FlagValues, "core-only")

	installDir := cmdctx.GetString(ctx.FlagValues, "install-dir")
	if installDir == "" {
		installDir = installDefaultDir
	}
	installDir = hbase.ExpandHomeDir(installDir)

	if err := checkRunningFromInstallDir(installDir); err != nil {
		return err
	}

	var err error
	version, err = resolveVersion(version)
	if err != nil {
		return err
	}

	platform, err := detectPlatform()
	if err != nil {
		return err
	}
	hlog.Debug("platform detected", "platform", platform)

	if check {
		exists, err := releaseAssetExists(version, platform, installBundleName)
		if err != nil {
			return err
		}
		if !exists {
			fmt.Printf("Version %s not found\n", version)
			os.Exit(1)
		}
		current := hbase.Version
		cmp, ok := cmpVersion(version, current)
		if !ok || cmp > 0 {
			fmt.Printf("Upgrade available: %s (current: %s)\n", version, current)
		} else if cmp < 0 {
			fmt.Printf("Current version %s is ahead of latest %s\n", current, version)
		} else {
			fmt.Printf("harness is up to date (current: %s)\n", current)
		}
		return nil
	}

	installCore := true
	if !force {
		current := hbase.Version
		if cmp, ok := cmpVersion(version, current); ok && cmp <= 0 {
			if cmp < 0 {
				fmt.Printf("Core is ahead of latest (current: %s, latest: %s). Use --force to reinstall.\n", current, version)
			} else {
				fmt.Printf("Core is up to date (current: %s, latest: %s).\n", current, version)
			}
			installCore = false
		}
	}

	if installCore {
		if err := os.MkdirAll(installDir, 0755); err != nil {
			return fmt.Errorf("creating install directory %s: %w", installDir, err)
		}
		hlog.Info("downloading", "version", version, "platform", platform)
		if err := downloadAndInstallBinary(version, platform, installDir, installBundleName, installBinaryName); err != nil {
			return err
		}
		fmt.Printf("Installed harness %s to %s/%s\n", version, installDir, installBinaryName)
	}

	if coreOnly {
		return nil
	}

	// Bring any installed plugins up to the version we just installed. Plugins
	// live in ~/.harness/bin and are tracked by their spec, not by sitting next
	// to core, so this no longer depends on installDir.
	for _, name := range installedRegistryPlugins() {
		if err := installRegistryPlugin(name, version, force, false); err != nil {
			fmt.Printf("warning: could not update plugin %q: %v\n", name, err)
		}
	}

	return nil
}

// InstallModuleHandler installs a module that ships as a plugin. "module" is the
// feature-area axis and "plugin" is the deployment-type axis; a module that
// isn't compiled in is installed exactly like any other plugin, so this hands
// off to the one install path rather than duplicating it.
func InstallModuleHandler(ctx *cmdctx.Ctx) error {
	moduleName := ctx.Id
	if moduleName == "" {
		return fmt.Errorf("module name is required (supported: %s)", registryNames())
	}
	if _, ok := pluginRegistry[moduleName]; !ok {
		return fmt.Errorf("unknown module %q — supported: %s", moduleName, registryNames())
	}
	version := cmdctx.GetString(ctx.FlagValues, "version")
	force := cmdctx.GetBool(ctx.FlagValues, "force")
	check := cmdctx.GetBool(ctx.FlagValues, "check")
	return installRegistryPlugin(moduleName, version, force, check)
}

func detectPlatform() (string, error) {
	var os_, arch string
	switch runtime.GOOS {
	case "darwin":
		os_ = "darwin"
	case "linux":
		os_ = "linux"
	default:
		return "", fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
	switch runtime.GOARCH {
	case "amd64":
		arch = "amd64"
	case "arm64":
		arch = "arm64"
	default:
		return "", fmt.Errorf("unsupported architecture: %s", runtime.GOARCH)
	}
	return os_ + "_" + arch, nil
}

// releaseURLs returns the tarball and checksum-file URLs for a package in the
// Harness release matching version. Shared by the core install and the plugin
// install so both derive artifact URLs the same way.
func releaseURLs(version, platform, pkgName string) (tarURL, checksumURL string) {
	ver := strings.TrimPrefix(version, "v")
	base := fmt.Sprintf("%s_%s_%s", pkgName, ver, platform)
	tarURL = fmt.Sprintf("https://github.com/%s/releases/download/%s/%s.tar.gz", release.Repo, version, base)
	checksumURL = fmt.Sprintf("https://github.com/%s/releases/download/%s/%s_%s_checksums.txt", release.Repo, version, installBinaryName, ver)
	return tarURL, checksumURL
}

func downloadAndInstallBinary(version, platform, destDir, pkgName, binaryName string) error {
	ver := strings.TrimPrefix(version, "v")
	base := fmt.Sprintf("%s_%s_%s", pkgName, ver, platform)
	tarURL, checksumURL := releaseURLs(version, platform, pkgName)

	tmp, err := os.MkdirTemp("", "harness-install-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmp)

	archivePath := filepath.Join(tmp, base+".tar.gz")
	if err := downloadFile(archivePath, tarURL); err != nil {
		if strings.Contains(err.Error(), "HTTP 404") {
			return fmt.Errorf("%s %s not found", pkgName, version)
		}
		return fmt.Errorf("downloading release: %w", err)
	}

	hlog.Debug("verifying checksum")
	if err := verifyChecksum(archivePath, base+".tar.gz", checksumURL); err != nil {
		return fmt.Errorf("checksum verification failed: %w", err)
	}

	binaryPath := filepath.Join(tmp, binaryName)
	if err := extractBinaryFromTar(archivePath, binaryName, binaryPath); err != nil {
		return fmt.Errorf("extracting binary: %w", err)
	}

	dest := filepath.Join(destDir, binaryName)
	staging := dest + ".new"
	if err := os.Rename(binaryPath, staging); err != nil {
		return fmt.Errorf("staging binary: %w", err)
	}
	if err := os.Chmod(staging, 0755); err != nil {
		os.Remove(staging)
		return fmt.Errorf("setting permissions: %w", err)
	}
	if err := os.Rename(staging, dest); err != nil {
		os.Remove(staging)
		return fmt.Errorf("installing binary: %w", err)
	}
	return nil
}

func releaseAssetExists(version, platform, pkgName string) (bool, error) {
	ver := strings.TrimPrefix(version, "v")
	base := fmt.Sprintf("%s_%s_%s", pkgName, ver, platform)
	client := &http.Client{Timeout: 15 * time.Second}
	url := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s.tar.gz", release.Repo, version, base)
	hlog.Debug("HEAD", "url", url)
	resp, err := client.Head(url)
	if err != nil {
		hlog.Debug("HEAD failed", "url", url, "error", err)
		return false, nil
	}
	resp.Body.Close()
	hlog.Debug("HEAD response", "url", url, "status", resp.StatusCode)
	return resp.StatusCode == http.StatusOK, nil
}

func downloadFile(dest, url string) error {
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

func verifyChecksum(archivePath, archiveName, checksumURL string) error {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(checksumURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d fetching checksums", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var expected string
	for _, line := range strings.Split(string(body), "\n") {
		if strings.Contains(line, archiveName) {
			expected = strings.Fields(line)[0]
			break
		}
	}
	if expected == "" {
		return fmt.Errorf("checksum entry not found for %s", archiveName)
	}

	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	actual := fmt.Sprintf("%x", h.Sum(nil))
	if actual != expected {
		return fmt.Errorf("checksum mismatch (expected %s, got %s)", expected, actual)
	}
	return nil
}

func extractBinaryFromTar(archivePath, binaryName, dest string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag == tar.TypeReg && filepath.Base(hdr.Name) == binaryName {
			out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
			if err != nil {
				return err
			}
			_, err = io.Copy(out, tr)
			out.Close()
			return err
		}
	}
	return fmt.Errorf("binary %q not found in archive", binaryName)
}
