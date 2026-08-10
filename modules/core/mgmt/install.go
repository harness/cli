// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package mgmt

import (
	"archive/tar"
	"archive/zip"
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

// resolveReleaseForPrefix validates version, resolves the release in repo
// tagged "{prefix}/vX.Y.Z" (bare "vX.Y.Z" when prefix == "") matching it, and
// returns that release together with its version string (the tag with prefix
// stripped). version == "" / "latest" resolves to that prefix's own latest
// release — independent of every other prefix's version in repo.
//
// This is the one place both the core install and every registry plugin
// install resolve a version, so there is exactly one algorithm for "latest"
// and exactly one for validating an explicit version.
//
// token, when non-empty, is forwarded to the GitHub API as a bearer token —
// it's how --github-token lets a plugin install see draft releases or
// releases in a private repo, neither visible to an unauthenticated request.
// allowDrafts additionally includes draft releases when resolving "latest".
func resolveReleaseForPrefix(repo, prefix, version, token string, allowDrafts bool) (*release.Release, string, error) {
	if version != "" && version != "latest" {
		if !strings.HasPrefix(version, "v") {
			version = "v" + version
		}
		if !reReleaseVersion.MatchString(version) {
			return nil, "", fmt.Errorf("invalid version %q — expected vMAJOR.MINOR.PATCH (e.g. v1.2.3) or \"latest\"", version)
		}
	} else {
		version = ""
	}
	hlog.Debug("resolving release", "repo", repo, "prefix", prefix, "version", version)
	rel, err := release.ResolveRelease(repo, prefix, version, token, allowDrafts)
	if err != nil {
		return nil, "", fmt.Errorf("resolving release: %w", err)
	}
	resolvedVersion := rel.TagName
	if prefix != "" {
		resolvedVersion = strings.TrimPrefix(resolvedVersion, prefix+"/")
	}
	hlog.Debug("resolved release", "tag", rel.TagName, "version", resolvedVersion)
	return rel, resolvedVersion, nil
}

// versionLabel renders a possibly-empty/"latest" version request for a
// diagnostic message.
func versionLabel(version string) string {
	if version == "" {
		return "latest"
	}
	return version
}

const (
	installBinaryName = "harness"
	installBundleName = "harness-core"
)

// defaultInstallDir is where the core binary lands when --install-dir is not
// given. On Windows there is no ~/.local/bin convention, so we use the per-user
// Programs dir, which needs no admin rights. Note this directory is not on PATH
// by default there — install.ps1 adds it.
func defaultInstallDir() (string, error) {
	if runtime.GOOS != "windows" {
		return "~/.local/bin", nil
	}
	if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
		return filepath.Join(localAppData, "Programs", "harness"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("determining default install directory: LOCALAPPDATA is not set and the home directory could not be determined: %w", err)
	}
	return filepath.Join(home, "AppData", "Local", "Programs", "harness"), nil
}

// installedBinaryName returns the file name a binary is stored under on this
// platform: Windows needs the .exe suffix for the OS to consider it executable.
func installedBinaryName(base string) string {
	if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(base), ".exe") {
		return base + ".exe"
	}
	return base
}

// archiveExtensionForPlatform returns the release archive extension for a
// platform string ("windows_amd64", "linux_arm64", …). Windows archives are zip
// per .goreleaser.yaml's format_overrides; everything else is tar.gz.
func archiveExtensionForPlatform(platform string) string {
	if strings.HasPrefix(platform, "windows_") {
		return ".zip"
	}
	return ".tar.gz"
}

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

	var err error
	installDir := cmdctx.GetString(ctx.FlagValues, "install-dir")
	if installDir == "" {
		installDir, err = defaultInstallDir()
		if err != nil {
			return err
		}
	}
	installDir = hbase.ExpandHomeDir(installDir)

	if err := checkRunningFromInstallDir(installDir); err != nil {
		return err
	}

	platform, err := detectPlatform()
	if err != nil {
		return err
	}
	hlog.Debug("platform detected", "platform", platform)

	rel, version, err := resolveReleaseForPrefix(release.Repo, "", version, "", false)
	if err != nil {
		return err
	}

	if check {
		if _, err := archiveAssetURL(rel, installBundleName, version, platform); err != nil {
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
		if err := downloadAndInstallBinary(rel, installBundleName, version, platform, installDir, installBinaryName, ""); err != nil {
			return err
		}
		fmt.Printf("Installed harness %s to %s\n", version, filepath.Join(installDir, installedBinaryName(installBinaryName)))
	}

	if coreOnly {
		return nil
	}

	// Bring any installed plugins up to their own latest — each module releases
	// independently of core, so this must not force-pin them to core's version.
	for _, name := range installedRegistryPlugins() {
		if err := installRegistryPlugin(name, "", "", false, force, false); err != nil {
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
	githubToken := cmdctx.GetString(ctx.FlagValues, "github-token")
	allowDrafts := cmdctx.GetBool(ctx.FlagValues, "allow-drafts")
	return installRegistryPlugin(moduleName, version, githubToken, allowDrafts, force, check)
}

func detectPlatform() (string, error) {
	var os_, arch string
	switch runtime.GOOS {
	case "darwin":
		os_ = "darwin"
	case "linux":
		os_ = "linux"
	case "windows":
		os_ = "windows"
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

// archiveAsset finds the asset in rel matching pkgName/version/platform. Every
// release we control produces exactly this asset naming convention, so a miss
// means the release itself is broken, not that verification should be skipped.
func archiveAsset(rel *release.Release, pkgName, version, platform string) (*release.Asset, error) {
	ver := strings.TrimPrefix(version, "v")
	name := fmt.Sprintf("%s_%s_%s%s", pkgName, ver, platform, archiveExtensionForPlatform(platform))
	for i := range rel.Assets {
		if rel.Assets[i].Name == name {
			return &rel.Assets[i], nil
		}
	}
	return nil, fmt.Errorf("%s %s has no asset %s for platform %s", pkgName, rel.TagName, name, platform)
}

// archiveAssetURL is a convenience wrapper for callers that only need the
// unauthenticated download URL (drift/availability checks never download).
func archiveAssetURL(rel *release.Release, pkgName, version, platform string) (string, error) {
	a, err := archiveAsset(rel, pkgName, version, platform)
	if err != nil {
		return "", err
	}
	return a.BrowserDownloadURL, nil
}

// checksumAsset finds the checksums file among rel's assets. Every release we
// produce ships one; a missing checksum on a release we control is an error,
// not "nothing to verify against."
func checksumAsset(rel *release.Release) (*release.Asset, error) {
	for i := range rel.Assets {
		if strings.HasSuffix(rel.Assets[i].Name, "checksums.txt") {
			return &rel.Assets[i], nil
		}
	}
	return nil, fmt.Errorf("release %s has no checksums file", rel.TagName)
}

// checksumAssetURL is the unauthenticated-URL counterpart of archiveAssetURL.
func checksumAssetURL(rel *release.Release) (string, error) {
	a, err := checksumAsset(rel)
	if err != nil {
		return "", err
	}
	return a.BrowserDownloadURL, nil
}

func downloadAndInstallBinary(rel *release.Release, pkgName, version, platform, destDir, binaryName, githubToken string) error {
	ext := archiveExtensionForPlatform(platform)
	archiveAsset, err := archiveAsset(rel, pkgName, version, platform)
	if err != nil {
		return err
	}
	checksumAsset, err := checksumAsset(rel)
	if err != nil {
		return err
	}

	tmp, err := os.MkdirTemp("", "harness-install-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmp)

	archivePath := filepath.Join(tmp, archiveAsset.Name)
	if err := downloadAsset(archivePath, archiveAsset, githubToken); err != nil {
		return fmt.Errorf("downloading release: %w", err)
	}

	hlog.Debug("verifying checksum")
	if err := verifyChecksum(archivePath, archiveAsset.Name, checksumAsset, githubToken); err != nil {
		return fmt.Errorf("checksum verification failed: %w", err)
	}

	memberName := installedBinaryName(binaryName)
	binaryPath := filepath.Join(tmp, memberName)
	if err := extractBinaryFromArchive(archivePath, memberName, binaryPath, ext); err != nil {
		return fmt.Errorf("extracting binary: %w", err)
	}

	dest := filepath.Join(destDir, memberName)
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

func downloadFile(dest, url string) error {
	resp, err := getUnauthenticated(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return saveResponseBody(dest, resp)
}

// downloadAsset saves asset to dest. With a token, it goes through GitHub's
// authenticated asset API (asset.URL) instead of BrowserDownloadURL — that's
// the only way to fetch a draft release's assets or a private repo's assets;
// BrowserDownloadURL 404s for both regardless of any Authorization header.
func downloadAsset(dest string, asset *release.Asset, githubToken string) error {
	resp, err := getAsset(asset, githubToken)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return saveResponseBody(dest, resp)
}

func saveResponseBody(dest string, resp *http.Response) error {
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

// getAsset issues the GET for asset, authenticated via the asset API when
// githubToken is set, or the plain browser-download URL otherwise.
func getAsset(asset *release.Asset, githubToken string) (*http.Response, error) {
	if githubToken == "" {
		return getUnauthenticated(asset.BrowserDownloadURL)
	}
	req, err := http.NewRequest("GET", asset.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Set("Authorization", "Bearer "+githubToken)
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, asset.URL)
	}
	return resp, nil
}

// getUnauthenticated is a plain GET, used for browser-download URLs and for
// anything with no token in hand.
func getUnauthenticated(url string) (*http.Response, error) {
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	return resp, nil
}

func verifyChecksum(archivePath, archiveName string, checksumAsset *release.Asset, githubToken string) error {
	resp, err := getAsset(checksumAsset, githubToken)
	if err != nil {
		return fmt.Errorf("fetching checksums: %w", err)
	}
	defer resp.Body.Close()
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

// extractBinaryFromArchive pulls binaryName out of archivePath, dispatching on
// the archive format ext (".zip" for Windows releases, tar.gz otherwise).
func extractBinaryFromArchive(archivePath, binaryName, dest, ext string) error {
	if ext == ".zip" {
		return extractBinaryFromZip(archivePath, binaryName, dest)
	}
	return extractBinaryFromTar(archivePath, binaryName, dest)
}

func extractBinaryFromZip(archivePath, binaryName, dest string) error {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		if f.FileInfo().IsDir() || filepath.Base(f.Name) != binaryName {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
		if err != nil {
			rc.Close()
			return err
		}
		_, copyErr := io.Copy(out, rc)
		rc.Close()
		if closeErr := out.Close(); closeErr != nil && copyErr == nil {
			copyErr = closeErr
		}
		if copyErr != nil {
			return copyErr
		}
		return nil
	}
	return fmt.Errorf("binary %q not found in archive", binaryName)
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
