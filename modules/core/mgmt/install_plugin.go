// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package mgmt

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/hbase"
	"github.com/harness/cli/pkg/hlog"
	"github.com/harness/cli/pkg/plugin"
	"github.com/harness/cli/pkg/release"
	"github.com/harness/cli/pkg/specloader"
)

// GithubPluginRef identifies a plugin release on GitHub: the "owner/repo" it
// publishes to, and the tag prefix its releases are published under ("" means
// core's own bare "vX.Y.Z" convention rather than "{TagPrefix}/vX.Y.Z").
// PkgName is the release package name when already known — always true for a
// registry entry. For a ref parsed from owner/repo[/prefix] syntax it starts
// empty and is either discovered from the release's own assets, or supplied
// via --plugin-name.
type GithubPluginRef struct {
	GithubRepo string
	TagPrefix  string
	PkgName    string
}

// pluginRegistry is the optional name→artifact resolver behind the bare-name
// install form (`install plugin har`, `install module har`). It starts as a
// hardcoded map and can later become a hosted index with no change to the
// install mechanism — nothing is *gated* by it, since URL and path installs
// cover secret/internal/third-party plugins.
//
// Every entry releases independently of core and of each other, each under its
// own "{TagPrefix}/vX.Y.Z" tag on release.Repo — a module shipping a fix does
// not wait on core's release cadence.
var pluginRegistry = map[string]GithubPluginRef{
	"har": {GithubRepo: release.Repo, TagPrefix: "har", PkgName: "harness-plugin-har"},
}

func registryNames() string {
	names := make([]string, 0, len(pluginRegistry))
	for k := range pluginRegistry {
		names = append(names, k)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// pluginRef is the parsed shape of an install-plugin <ref> argument — exactly
// one of URL, LocalPath, PluginName, or GithubRef is set.
type pluginRef struct {
	URL        string
	LocalPath  string
	PluginName string
	GithubRef  *GithubPluginRef
}

// parsePluginRef classifies an install-plugin <ref> argument as a tarball URL,
// a local path, a GitHub plugin ref parsed from owner/repo[/prefix] syntax, or
// a bare plugin name — the latter left for the caller to resolve against
// pluginRegistry, since a registry name and a raw GitHub ref are resolved
// differently (e.g. --plugin-name is meaningless for the former). It touches
// no filesystem or network, so every branch of the classification is
// unit-testable on its own.
func parsePluginRef(ref string) (pluginRef, error) {
	switch {
	case strings.HasPrefix(ref, "http://"), strings.HasPrefix(ref, "https://"):
		return pluginRef{URL: ref}, nil
	case looksLikePath(ref):
		return pluginRef{LocalPath: ref}, nil
	case isArchive(ref):
		// Doesn't match looksLikePath, but the extension makes the intent
		// clear enough to give a precise fix instead of trying (and failing)
		// a GitHub ref lookup or an "unknown plugin name" error.
		return pluginRef{}, fmt.Errorf("%q looks like a plugin archive, but is not a path harness recognizes — local paths must be absolute, or start with \"./\" or \"~/\"", ref)
	case strings.Contains(ref, "/"):
		owner, repoName, prefix, ok := splitGitHubRef(ref)
		if !ok {
			return pluginRef{}, fmt.Errorf("%q is not a valid owner/repo or owner/repo/prefix GitHub ref", ref)
		}
		return pluginRef{GithubRef: &GithubPluginRef{GithubRepo: owner + "/" + repoName, TagPrefix: prefix}}, nil
	case plugin.ValidateName(ref) == nil:
		// Bare name → registry lookup. Same path `install module` takes.
		return pluginRef{PluginName: ref}, nil
	default:
		return pluginRef{}, fmt.Errorf("%q is not a URL, an existing file, an owner/repo ref, or a valid plugin name — supported names: %s", ref, registryNames())
	}
}

// InstallPluginHandler installs a plugin from a tarball URL, a local tarball, a
// local binary, an owner/repo[/prefix] GitHub ref, or a bare registry name. All
// forms funnel into the same routine: get a binary on disk → identity gate
// (--identity) → install into ~/.harness/bin → capture grammar (--spec) →
// write <name>.spec.yaml with the host-owned provenance block. Core is the
// only writer of spec files.
func InstallPluginHandler(ctx *cmdctx.Ctx) error {
	ref := ctx.Id
	if ref == "" {
		return fmt.Errorf("install plugin requires a tarball URL, a local tarball or binary path, an owner/repo[/prefix] GitHub ref, or a plugin name (%s)", registryNames())
	}

	version := cmdctx.GetString(ctx.FlagValues, "version")
	force := cmdctx.GetBool(ctx.FlagValues, "force")
	check := cmdctx.GetBool(ctx.FlagValues, "check")
	githubToken := cmdctx.GetString(ctx.FlagValues, "github-token")
	allowDrafts := cmdctx.GetBool(ctx.FlagValues, "allow-drafts")
	pluginName := cmdctx.GetString(ctx.FlagValues, "plugin-name")

	if ref == "all" {
		return installAllRegistryPlugins(version, githubToken, allowDrafts, force, check)
	}

	parsed, err := parsePluginRef(ref)
	if err != nil {
		return err
	}

	switch {
	case parsed.URL != "":
		// A bare URL has no discoverable checksum file and no promised name.
		res, err := installPluginFromURL(parsed.URL, "", parsed.URL, "")
		if err != nil {
			return err
		}
		res.report()
		return nil
	case parsed.LocalPath != "":
		res, err := installPluginFromPath(parsed.LocalPath)
		if err != nil {
			return err
		}
		res.report()
		return nil
	case parsed.GithubRef != nil:
		gh := *parsed.GithubRef
		// --plugin-name disambiguates a release with more than one plugin
		// asset, since the pkgName here is never known ahead of time.
		if pluginName != "" {
			gh.PkgName = plugin.BinaryPrefix + "plugin-" + pluginName
		}
		return installPluginFromRelease(gh.GithubRepo, gh.TagPrefix, gh.PkgName, version, githubToken, allowDrafts, force, check)
	case parsed.PluginName != "":
		// A registry entry's asset is already pinned, so --plugin-name has
		// nothing to disambiguate.
		if pluginName != "" {
			return fmt.Errorf("--plugin-name is not valid with %q — its release asset name is already known", parsed.PluginName)
		}
		return installRegistryPlugin(parsed.PluginName, version, githubToken, allowDrafts, force, check)
	default:
		return fmt.Errorf("internal error: parsePluginRef(%q) returned no variant", ref)
	}
}

// looksLikePath reports whether ref should be treated as a filesystem path
// rather than a registry name or a GitHub ref. Only these three forms count —
// there is no filesystem probing here, so a bareword or a relative path
// without "./" is never silently treated as a path.
func looksLikePath(ref string) bool {
	return filepath.IsAbs(ref) || strings.HasPrefix(ref, "~/") || strings.HasPrefix(ref, "./")
}

// splitGitHubRef parses ref as "owner/repo" or "owner/repo/prefix". Any other
// slash count is rejected outright rather than falling through to be treated
// as a bare plugin name.
func splitGitHubRef(ref string) (owner, repoName, prefix string, ok bool) {
	parts := strings.Split(ref, "/")
	switch len(parts) {
	case 2:
		owner, repoName = parts[0], parts[1]
	case 3:
		owner, repoName, prefix = parts[0], parts[1], parts[2]
	default:
		return "", "", "", false
	}
	if owner == "" || repoName == "" || (len(parts) == 3 && prefix == "") {
		return "", "", "", false
	}
	return owner, repoName, prefix, true
}

// installRegistryPlugin installs a plugin named in pluginRegistry. Its repo is
// always release.Repo and its pkgName is always known, so it's a thin wrapper
// over installPluginFromRelease, which also serves the generic
// owner/repo[/prefix] ref form where the repo is user-supplied and pkgName may
// have to be discovered from the release's own assets.
func installRegistryPlugin(name, version, githubToken string, allowDrafts, force, check bool) error {
	entry, ok := pluginRegistry[name]
	if !ok {
		return fmt.Errorf("unknown plugin %q — supported: %s\n\nTo install an unregistered plugin, pass its tarball URL, path, or owner/repo ref", name, registryNames())
	}
	return installPluginFromRelease(entry.GithubRepo, entry.TagPrefix, entry.PkgName, version, githubToken, allowDrafts, force, check)
}

// installAllRegistryPlugins updates every registry-known plugin that is
// currently installed to its own latest release. "all" has no single version
// to install — each plugin releases independently of core and of every other
// plugin — so a caller-supplied --version is rejected rather than silently
// ignored or applied to every plugin. --force is rejected too: forcing a
// reinstall of every plugin regardless of its own up-to-date state is a
// per-plugin decision, made by naming that plugin directly. --github-token
// and --allow-drafts are rejected as well — every registry plugin releases
// from release.Repo under core's own visible, non-draft release process, so
// neither flag has anything to do for a registry name.
func installAllRegistryPlugins(version, githubToken string, allowDrafts, force, check bool) error {
	if version != "" && version != "latest" {
		return fmt.Errorf(`--version is not valid with "all" — each plugin releases independently, so there is no single version to install`)
	}
	if force {
		return fmt.Errorf(`--force is not valid with "all" — install a plugin by name to force its reinstall`)
	}
	if githubToken != "" {
		return fmt.Errorf(`--github-token is not valid with "all" — registry plugins are never behind a private or draft release`)
	}
	if allowDrafts {
		return fmt.Errorf(`--allow-drafts is not valid with "all" — registry plugins are never behind a private or draft release`)
	}
	names := installedRegistryPlugins()
	if len(names) == 0 {
		fmt.Println("no registry plugins installed")
		return nil
	}
	var failed []string
	for _, name := range names {
		if err := installRegistryPlugin(name, "", "", false, false, check); err != nil {
			fmt.Printf("warning: could not install plugin %q: %v\n", name, err)
			failed = append(failed, name)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("failed to install %d of %d plugin(s): %s", len(failed), len(names), strings.Join(failed, ", "))
	}
	return nil
}

// installPluginFromRelease resolves a release tagged {prefix}/vX.Y.Z ("" /
// "latest" resolves to that prefix's own latest release) in repo, finds the
// plugin asset in it, and installs it — checking drift against any existing
// install first. check reports availability and drift without installing.
//
// pkgName, when known (the registry form), selects the asset directly.
// When empty (a generic owner/repo[/prefix] ref, where no pkgName is known
// ahead of time), the asset — and the plugin's name — is discovered by
// pattern-matching the release's own assets.
//
// This is the only ref form that does an up-to-date check: naming a release
// this way means "get me the current one", so reinstalling an identical
// version is wasted work. An explicit URL or path names a specific artifact
// and is always installed.
//
// githubToken, when set, is forwarded to release resolution and asset download
// so this can also install from a draft release or a private repo; allowDrafts
// additionally includes drafts when resolving "latest" (meaningless without a
// token, since an unauthenticated request never sees a draft to begin with).
func installPluginFromRelease(repo, prefix, pkgName, version, githubToken string, allowDrafts, force, check bool) error {
	platform, err := detectPlatform()
	if err != nil {
		return err
	}
	hlog.Debug("platform detected", "platform", platform)

	rel, resolvedVersion, err := resolveReleaseForPrefix(repo, prefix, version, githubToken, allowDrafts)
	if err != nil {
		return fmt.Errorf("version %s not found in %s: %w", versionLabel(version), repo, err)
	}

	var asset *release.Asset
	var name string
	if pkgName != "" {
		name = strings.TrimPrefix(pkgName, plugin.BinaryPrefix+"plugin-")
		asset, err = archiveAsset(rel, pkgName, resolvedVersion, platform)
	} else {
		asset, name, err = discoverPluginAsset(rel, resolvedVersion, platform)
	}
	if err != nil {
		return fmt.Errorf("%s %s not available for platform %s: %w", repo, resolvedVersion, platform, err)
	}

	installed := installedPluginVersion(name)
	// A spec whose binary is gone is not an install, whatever version it
	// records — treat it as absent so the up-to-date gate below doesn't refuse
	// to replace a binary that isn't there.
	if installed != "" && !pluginBinaryPresent(name) {
		suffix := ""
		if !check {
			suffix = " — reinstalling"
		}
		fmt.Printf("Plugin %q spec records version %s but its binary is missing%s\n", name, installed, suffix)
		installed = ""
	}

	if check {
		if installed == "" {
			fmt.Printf("Plugin %q %s is available to install\n", name, resolvedVersion)
			return nil
		}
		cmp, ok := cmpVersion(resolvedVersion, installed)
		switch {
		case !ok:
			fmt.Printf("Plugin %q is installed (current: %s, latest: %s)\n", name, installed, resolvedVersion)
		case cmp > 0:
			fmt.Printf("Upgrade available for plugin %q: %s (current: %s)\n", name, resolvedVersion, installed)
		case cmp < 0:
			fmt.Printf("Current version %s of plugin %q is ahead of latest %s\n", installed, name, resolvedVersion)
		default:
			fmt.Printf("Plugin %q is up to date (current: %s)\n", name, installed)
		}
		return nil
	}

	if !force && installed != "" {
		if cmp, ok := cmpVersion(resolvedVersion, installed); ok && cmp <= 0 {
			if cmp < 0 {
				fmt.Printf("Plugin %q is ahead of latest (installed: %s, latest: %s). Use --force to reinstall.\n", name, installed, resolvedVersion)
			} else {
				fmt.Printf("Plugin %q is up to date (installed: %s, latest: %s). Use --force to reinstall.\n", name, installed, resolvedVersion)
			}
			return nil
		}
	}

	checksumAsset, err := checksumAsset(rel)
	if err != nil {
		return err
	}
	hlog.Info("downloading plugin", "plugin", name, "version", resolvedVersion, "platform", platform)
	// expectName: the release promised us this plugin (by registry entry or by
	// asset-name discovery), so a tarball that identifies as something else is
	// a bad release, not a new plugin — and must be rejected before it lands
	// anywhere on disk.
	res, err := installPluginFromAsset(asset, checksumAsset, githubToken, name)
	if err != nil {
		return err
	}
	res.report()
	return nil
}

// discoverPluginAsset finds the sole plugin asset in rel for version and
// platform when pkgName isn't known ahead of time (the generic
// owner/repo[/prefix] ref form). It pattern-matches every asset against
// harness-plugin-<name>_<version>_<platform>{.tar.gz,.tgz,.zip} and returns
// the one match together with the <name> it captured — that name becomes both
// the identity-gate expectation and the plugin's spec/version-tracking key.
//
// Zero matches means the release has no plugin for this platform; two or more
// means the release ships more than one plugin and --plugin-name is needed to
// pick one.
func discoverPluginAsset(rel *release.Release, version, platform string) (*release.Asset, string, error) {
	ver := strings.TrimPrefix(version, "v")
	suffix := fmt.Sprintf("_%s_%s%s", ver, platform, archiveExtensionForPlatform(platform))

	type candidate struct {
		asset *release.Asset
		name  string
	}
	var matches []candidate
	for i := range rel.Assets {
		a := &rel.Assets[i]
		m := pluginAssetNamePattern.FindStringSubmatch(a.Name)
		if m == nil || !strings.HasSuffix(a.Name, suffix) {
			continue
		}
		matches = append(matches, candidate{asset: a, name: m[1]})
	}

	switch len(matches) {
	case 1:
		return matches[0].asset, matches[0].name, nil
	case 0:
		return nil, "", fmt.Errorf("no plugin asset for platform %s in release %s", platform, rel.TagName)
	}
	names := make([]string, len(matches))
	for i, m := range matches {
		names[i] = m.name
	}
	sort.Strings(names)
	return nil, "", fmt.Errorf("release %s has %d plugin candidates for platform %s (%s) — pass --plugin-name to pick one",
		rel.TagName, len(matches), platform, strings.Join(names, ", "))
}

// pluginAssetNamePattern captures <name> out of a harness-plugin-<name>_...
// release asset name — the naming convention every plugin release, registered
// or not, is expected to follow.
var pluginAssetNamePattern = regexp.MustCompile(`^` + regexp.QuoteMeta(plugin.BinaryPrefix+"plugin-") + `([a-z0-9]+(?:-[a-z0-9]+)*)_`)

// installedPlugin is what a successful install produces: the gated identity and
// the path the binary actually landed at.
type installedPlugin struct {
	id      *plugin.Identity
	binPath string
}

func (p installedPlugin) report() {
	fmt.Printf("Installed plugin %q %s to %s\n", p.id.Name, p.id.Version, p.binPath)
}

// installPluginFromURL downloads a tarball (verifying its checksum when
// checksumURL is set) and installs the plugin binary inside it.
func installPluginFromURL(tarURL, checksumURL, source, expectName string) (*installedPlugin, error) {
	tmp, err := os.MkdirTemp("", "harness-plugin-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmp)

	archivePath := filepath.Join(tmp, filepath.Base(tarURL))
	if err := downloadFile(archivePath, tarURL); err != nil {
		if strings.Contains(err.Error(), "HTTP 404") {
			return nil, fmt.Errorf("no plugin tarball at %s", tarURL)
		}
		return nil, fmt.Errorf("downloading plugin: %w", err)
	}
	if checksumURL != "" {
		hlog.Debug("verifying checksum")
		if err := verifyChecksum(archivePath, filepath.Base(archivePath), &release.Asset{BrowserDownloadURL: checksumURL}, ""); err != nil {
			return nil, fmt.Errorf("checksum verification failed: %w", err)
		}
	}
	return installPluginFromTarball(archivePath, tmp, source, expectName)
}

// installPluginFromAsset is installPluginFromURL's counterpart for a registry
// install: it downloads through the asset-aware, token-capable path so a
// draft release or private repo's assets can be fetched, not just resolved.
func installPluginFromAsset(archiveAsset, checksumAsset *release.Asset, githubToken, expectName string) (*installedPlugin, error) {
	tmp, err := os.MkdirTemp("", "harness-plugin-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmp)

	archivePath := filepath.Join(tmp, archiveAsset.Name)
	if err := downloadAsset(archivePath, archiveAsset, githubToken); err != nil {
		return nil, fmt.Errorf("downloading plugin: %w", err)
	}
	hlog.Debug("verifying checksum")
	if err := verifyChecksum(archivePath, archiveAsset.Name, checksumAsset, githubToken); err != nil {
		return nil, fmt.Errorf("checksum verification failed: %w", err)
	}
	return installPluginFromTarball(archivePath, tmp, archiveAsset.BrowserDownloadURL, expectName)
}

// installPluginFromPath installs from a local tarball or a local plugin binary.
func installPluginFromPath(ref string) (*installedPlugin, error) {
	path, err := filepath.Abs(hbase.ExpandHomeDir(ref))
	if err != nil {
		return nil, fmt.Errorf("resolving %q: %w", ref, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("no file at %q: %w", path, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%q is a directory, not a plugin tarball or binary", path)
	}
	// No expected name: the user named an artifact directly, so whatever it
	// identifies as is what they asked for.
	if isArchive(path) {
		tmp, err := os.MkdirTemp("", "harness-plugin-*")
		if err != nil {
			return nil, fmt.Errorf("creating temp dir: %w", err)
		}
		defer os.RemoveAll(tmp)
		return installPluginFromTarball(path, tmp, path, "")
	}
	return installPluginBinary(path, path, "")
}

// isArchive reports whether path names a plugin archive rather than a bare
// plugin binary. Windows release archives are zip, so a ref that is not
// recognized here would be misread as a binary and fail at the identity gate.
func isArchive(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz") || strings.HasSuffix(lower, ".zip")
}

// installPluginFromTarball extracts the plugin binary from archivePath into
// workDir and installs it.
func installPluginFromTarball(archivePath, workDir, source, expectName string) (*installedPlugin, error) {
	stageDir := filepath.Join(workDir, "extract")
	if err := os.MkdirAll(stageDir, 0700); err != nil {
		return nil, fmt.Errorf("creating staging dir: %w", err)
	}
	binPath, err := extractPluginBinary(archivePath, stageDir)
	if err != nil {
		return nil, err
	}
	return installPluginBinary(binPath, source, expectName)
}

// installPluginBinary is the shared tail of every install form: check the file
// name against the required convention, gate the binary on its identity, copy it
// into ~/.harness/bin, capture its grammar, and write the spec with host-owned
// provenance.
//
// Both checks run before the copy so a binary that isn't a cooperating harness
// plugin never lands in the bin dir; --spec runs after, on the installed path,
// so the captured grammar comes from the binary that will actually be exec'd.
//
// The file name is required to be harness-<name> and to agree with the gated
// identity name. The convention is enforced here rather than only at tarball
// selection so that a plugin cannot be built and installed under a name that
// would make it unfindable once it ships in a release archive.
//
// expectName, when non-empty, is the name the caller was promised (a registry
// lookup). A mismatch is checked before the copy, so a bad registry entry can't
// install a different plugin than the one asked for.
func installPluginBinary(srcPath, source, expectName string) (*installedPlugin, error) {
	fileName, ok := plugin.NameFromBinary(srcPath)
	if !ok {
		return nil, fmt.Errorf("%s: plugin binary %q must be named %s<name> (lowercase alphanumeric, single dashes as separators)",
			source, filepath.Base(srcPath), plugin.BinaryPrefix)
	}
	id, err := plugin.QueryIdentity(srcPath)
	if err != nil {
		// QueryIdentity names no path, since srcPath may be a scratch extract
		// dir that means nothing to the user. Report the ref they gave us.
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	if id.Name != fileName {
		return nil, fmt.Errorf("%s: binary is named %q but identifies as plugin %q — the file name must be %s",
			source, filepath.Base(srcPath), id.Name, plugin.BinaryName(id.Name))
	}
	if expectName != "" && id.Name != expectName {
		return nil, fmt.Errorf("%s identifies as plugin %q, expected %q", source, id.Name, expectName)
	}
	hlog.Info("plugin identity verified", "name", id.Name, "version", id.Version)

	// Rebuild the destination name from the gated identity rather than reusing
	// the source name, keeping only the .exe suffix, which the OS needs.
	destName := plugin.BinaryName(id.Name)
	if strings.HasSuffix(filepath.Base(srcPath), ".exe") {
		destName += ".exe"
	}
	binPath, err := installToBinDir(srcPath, destName)
	if err != nil {
		return nil, err
	}

	grammar, err := exec.Command(binPath, "--spec").Output()
	if err != nil {
		return nil, fmt.Errorf("plugin %q failed to emit its spec (--spec): %w", id.Name, err)
	}
	if err := writePluginSpec(id, binPath, source, grammar); err != nil {
		return nil, err
	}
	return &installedPlugin{id: id, binPath: binPath}, nil
}

// installToBinDir copies src into ~/.harness/bin/<name>, staging then renaming
// so a concurrent dispatch never sees a half-written binary. Copying src onto
// itself is a no-op: that is the dev flow, where the binary is built directly
// into the bin dir and then installed from there.
func installToBinDir(src, name string) (string, error) {
	binDir := hbase.GetHarnessBinDir()
	if err := os.MkdirAll(binDir, 0700); err != nil {
		return "", fmt.Errorf("creating plugin bin dir %q: %w", binDir, err)
	}
	// Absolute: binary_path is dispatched from whatever directory the user
	// happens to be in, and HARNESS_CLI_HOME may itself be relative.
	binDir, err := filepath.Abs(binDir)
	if err != nil {
		return "", fmt.Errorf("resolving plugin bin dir %q: %w", binDir, err)
	}
	dest := filepath.Join(binDir, name)
	if sameFile(src, dest) {
		hlog.Debug("plugin binary already in place", "path", dest)
		if err := os.Chmod(dest, 0755); err != nil {
			return "", fmt.Errorf("setting permissions on %q: %w", dest, err)
		}
		return dest, nil
	}
	staging := dest + ".new"
	if err := copyFile(src, staging, 0755); err != nil {
		os.Remove(staging)
		return "", fmt.Errorf("staging plugin binary: %w", err)
	}
	if err := os.Rename(staging, dest); err != nil {
		os.Remove(staging)
		return "", fmt.Errorf("installing plugin binary %q: %w", dest, err)
	}
	hlog.Debug("installed plugin binary", "path", dest)
	return dest, nil
}

func sameFile(a, b string) bool {
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(ai, bi)
}

func copyFile(src, dest string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	// O_CREATE respects umask, so set the mode explicitly.
	if err := os.Chmod(dest, mode); err != nil {
		return err
	}
	return nil
}

// extractPluginBinary unpacks the plugin binary out of an archive into destDir
// and returns its path. The plugin binary is identified by name: exactly one
// regular file entry must be named harness-<name>[.exe]. Entry names are
// flattened to their base name, so a hostile archive cannot write outside
// destDir, and only the matching entry is written at all.
//
// Matching on the name rather than on the archive's exec bit means an archive
// built by a tool that does not preserve file modes still installs — the
// extracted file is chmod'd executable here regardless. Licenses, docs, and any
// co-bundled non-plugin binary (the core `harness` binary, notably) simply do
// not match.
func extractPluginBinary(archivePath, destDir string) (string, error) {
	var matched []string
	var err error
	if strings.HasSuffix(strings.ToLower(archivePath), ".zip") {
		matched, err = extractPluginEntriesFromZip(archivePath, destDir)
	} else {
		matched, err = extractPluginEntriesFromTar(archivePath, destDir)
	}
	if err != nil {
		return "", err
	}

	switch len(matched) {
	case 1:
		return filepath.Join(destDir, matched[0]), nil
	case 0:
		return "", fmt.Errorf("%s holds no file named %s<name> — not a harness plugin archive",
			filepath.Base(archivePath), plugin.BinaryPrefix)
	}
	sort.Strings(matched)
	return "", fmt.Errorf("%s holds %d plugin binaries (%s) — install one plugin at a time",
		filepath.Base(archivePath), len(matched), strings.Join(matched, ", "))
}

// extractPluginEntriesFromTar writes every tar entry whose base name conforms to
// the harness-<name> convention into destDir, returning the names written.
func extractPluginEntriesFromTar(archivePath, destDir string) ([]string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", filepath.Base(archivePath), err)
	}
	defer gzr.Close()

	var matched []string
	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		name := filepath.Base(hdr.Name)
		if _, ok := plugin.NameFromBinary(name); !ok {
			continue
		}
		if err := writeExtractedBinary(filepath.Join(destDir, name), tr); err != nil {
			return nil, err
		}
		matched = append(matched, name)
	}
	return matched, nil
}

// extractPluginEntriesFromZip is the zip counterpart of
// extractPluginEntriesFromTar, for Windows release archives.
func extractPluginEntriesFromZip(archivePath, destDir string) ([]string, error) {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", filepath.Base(archivePath), err)
	}
	defer r.Close()

	var matched []string
	for _, entry := range r.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		name := filepath.Base(entry.Name)
		if _, ok := plugin.NameFromBinary(name); !ok {
			continue
		}
		rc, err := entry.Open()
		if err != nil {
			return nil, err
		}
		err = writeExtractedBinary(filepath.Join(destDir, name), rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		matched = append(matched, name)
	}
	return matched, nil
}

// writeExtractedBinary copies an archive entry to dest as an executable. The
// mode is set explicitly after the write since O_CREATE respects umask, and
// because an archive built by a tool that drops file modes must still yield a
// runnable binary.
func writeExtractedBinary(dest string, src io.Reader) error {
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, src); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.Chmod(dest, 0755); err != nil {
		return err
	}
	return nil
}

// writePluginSpec writes ~/.harness/spec/<name>.spec.yaml: the plugin's own
// grammar plus the host-owned provenance block. The spec file is one YAML
// document, so provenance is decoded into the same mapping and re-encoded rather
// than spliced onto the grammar bytes.
func writePluginSpec(id *plugin.Identity, binPath, source string, grammar []byte) error {
	specDir := specloader.HomeSpecDir()
	if err := os.MkdirAll(specDir, 0700); err != nil {
		return fmt.Errorf("creating spec dir %q: %w", specDir, err)
	}
	if err := os.Chmod(specDir, 0700); err != nil {
		return fmt.Errorf("securing spec dir %q: %w", specDir, err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(grammar, &doc); err != nil {
		return fmt.Errorf("plugin %q emitted invalid spec YAML: %w", id.Name, err)
	}
	root := &doc
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		root = root.Content[0]
	}
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("plugin %q spec is not a YAML mapping", id.Name)
	}
	setMapField(root, "version", id.Version)
	setMapField(root, "binary_path", binPath)
	setMapField(root, "source", source)
	setMapField(root, "installed_at", time.Now().UTC().Format(time.RFC3339))

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return fmt.Errorf("encoding spec: %w", err)
	}
	specPath := filepath.Join(specDir, id.Name+".spec.yaml")
	if err := os.WriteFile(specPath, out, 0600); err != nil {
		return fmt.Errorf("writing spec %q: %w", specPath, err)
	}
	hlog.Debug("wrote plugin spec", "path", specPath, "binary", binPath)
	return nil
}

// installedPluginProvenance returns the version and binary path recorded in the
// installed spec for name, both "" when the plugin is not installed. The spec is
// the source of truth for what is installed, so this never execs the binary.
func installedPluginProvenance(name string) (version, binPath string) {
	path := filepath.Join(specloader.HomeSpecDir(), name+".spec.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	var f struct {
		Version    string `yaml:"version"`
		BinaryPath string `yaml:"binary_path"`
	}
	if err := yaml.Unmarshal(data, &f); err != nil {
		return "", ""
	}
	return f.Version, f.BinaryPath
}

func installedPluginVersion(name string) string {
	version, _ := installedPluginProvenance(name)
	return version
}

// pluginBinaryPresent reports whether the binary name's installed spec points at
// still exists. Kept separate from installedPluginVersion so that
// installedRegistryPlugins keeps listing a plugin whose binary has gone missing
// — `install cli` is how that plugin gets repaired.
func pluginBinaryPresent(name string) bool {
	_, binPath := installedPluginProvenance(name)
	if binPath == "" {
		return false
	}
	_, err := os.Stat(hbase.ExpandHomeDir(binPath))
	return err == nil
}

// installedRegistryPlugins returns the registry-known plugins that are currently
// installed, in a stable order. `install cli` uses this to keep Harness plugins
// in step with core; plugins installed by URL or path are deliberately excluded
// since we have no artifact to re-fetch them from.
func installedRegistryPlugins() []string {
	var names []string
	for name := range pluginRegistry {
		if installedPluginVersion(name) != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// setMapField sets key to a scalar value on a YAML mapping node, replacing an
// existing entry or appending a new key/value pair.
func setMapField(m *yaml.Node, key, value string) {
	val := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = val
			return
		}
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		val,
	)
}
