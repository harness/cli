// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package hbase

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/google/uuid"
)

// RunID is a UUID generated once at process startup. It is sent as
// X-Harness-CLI-Run-ID on every outgoing API call to correlate all
// requests from a single CLI invocation.
var RunID = uuid.New().String()

// overridden at build time via ldflags: -X github.com/harness/cli/pkg/hbase.Version=x.y.z
var Version = "0.1.0-dev"

// overridden at build time via ldflags: -X github.com/harness/cli/pkg/hbase.BuildTime=yyyymmddhhmmZ
var BuildTime = ""

// TimeoutExitCode is the exit code used when a command is killed by --timeout.
const TimeoutExitCode = 124

// Hidden flags. See the corresponding rootcmd.MaybeRun* function for each.
const (
	PostUpgradeFlag           = "--post-upgrade"
	PostInstallFlag           = "--post-install"
	BackgroundUpdateCheckFlag = "--background-update-check"
)

const (
	HarnessHome         = "~/.harness"
	ConfigFileName      = "config.yaml"
	CredentialsFileName = "credentials"

	// EnvCheckSpecs triggers spec validation mode when set to "1".
	EnvCheckSpecs = "HARNESS_CHECKSPECS"

	// EnvDebugCompletion enables debug logging for completion invocations, writing to CompletionDebugLogFile.
	EnvDebugCompletion = "HARNESS_DEBUG_COMPLETION"

	// EnvPipelineID is set by the Harness platform when running inside a pipeline.
	EnvPipelineID = "HARNESS_PIPELINEID"

	// EnvNoUpdateCheck disables the background update check when set to "1".
	EnvNoUpdateCheck = "HARNESS_NO_UPDATE_CHECK"

	// EnvNoTelemetry disables all usage telemetry when set to "1".
	EnvNoTelemetry = "HARNESS_NO_TELEMETRY"

	// EnvInstallType identifies how the CLI was installed (e.g. "script"),
	// set by the installer before invoking --post-install. See
	// [telemetry.ResolveInstallType] for the whitelist and default.
	EnvInstallType = "HARNESS_INSTALL_TYPE"

	// EnvLogFile redirects all log output to the specified file path.
	EnvLogFile = "HARNESS_CLI_LOGFILE"

	// EnvColumns sets default --columns overrides per noun for list commands, so users
	// don't have to pass --columns on every invocation (e.g. to keep demo terminals from
	// wrapping columns). Format: semicolon-separated "noun=columns" entries, e.g.
	// "pipeline=id,name,status;execution=id,status,started". See [format.EnvColumnsFor].
	EnvColumns = "HARNESS_CLI_COLUMNS"

	// EnvCLIHome overrides the harness home directory (default ~/.harness).
	EnvCLIHome = "HARNESS_CLI_HOME"

	// Env var names for env-var auth mode.
	EnvAPIKey      = "HARNESS_API_KEY"
	EnvAPIJWT      = "HARNESS_API_JWT"
	EnvAccount     = "HARNESS_ACCOUNT"
	EnvAPIURL      = "HARNESS_API_URL"
	EnvOrg         = "HARNESS_ORG"
	EnvProject     = "HARNESS_PROJECT"
	EnvRegistryURL = "HARNESS_REGISTRY_URL"
	EnvProfile     = "HARNESS_PROFILE"

	// EnvSSOAuthServerURL overrides the SSO authorization server base URL
	// (default https://id.harness.io).
	EnvSSOAuthServerURL = "HARNESS_SSO_AUTH_SERVER_URL"

	// EnvSSOBaseURL overrides the SSO API base URL used for SSO-authenticated
	// requests (default https://mcp.harness.io/cli).
	EnvSSOBaseURL = "HARNESS_SSO_BASE_URL"

	// EnvSSOClientID overrides the SSO OAuth client ID
	// (default harness-cli-client).
	EnvSSOClientID = "HARNESS_SSO_CLIENT_ID"

	// Defaults applied when env vars are not set.
	DefaultAPIURL      = "https://app.harness.io"
	DefaultRegistryURL = "https://pkg.harness.io"
)

func GetCredentialsFilePath() string {
	return filepath.Join(GetHarnessHomeDir(), CredentialsFileName)
}

// CompletionDebugLogFile returns the path used for completion debug logging
// when EnvDebugCompletion is set. On Windows there's no /tmp, so it falls
// back to os.TempDir().
func CompletionDebugLogFile() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.TempDir(), "harness-completion-debug.log")
	}
	return "/tmp/harness-completion-debug.log"
}

// IsDev reports whether this is a development build (Version ends with "-dev").
func IsDev() bool {
	return strings.HasSuffix(Version, "-dev")
}

// IsPipelineExecution reports whether the CLI is running as a step inside a Harness pipeline.
func IsPipelineExecution() bool {
	return os.Getenv(EnvPipelineID) != ""
}

// GetHarnessHomeDir returns the harness home directory, defaulting to
// ~/.harness unless overridden by EnvCLIHome.
func GetHarnessHomeDir() string {
	if dir := os.Getenv(EnvCLIHome); dir != "" {
		return ExpandHomeDir(dir)
	}
	return ExpandHomeDir(HarnessHome)
}

func GetConfigFilePath() string {
	return filepath.Join(GetHarnessHomeDir(), ConfigFileName)
}

// GetHarnessBinDir returns the directory installed plugin binaries live in:
// ~/.harness/bin. This is a directory the CLI always owns and can write to,
// unlike the core binary's own directory (owned by brew/deb/rpm/scoop).
func GetHarnessBinDir() string {
	return filepath.Join(GetHarnessHomeDir(), "bin")
}

// BrewCaskRef is the tap-qualified cask this CLI is published as, suitable for
// `brew install --cask` / `brew upgrade --cask`.
const BrewCaskRef = "harness/tap/harness-cli"

// brewCaskDirName is the directory Homebrew stages cask payloads under:
// $HOMEBREW_PREFIX/Caskroom/<token>/<version>/<binary>, with a symlink to it
// from $HOMEBREW_PREFIX/bin. Matching a resolved path on this segment is
// prefix-independent (works for /opt/homebrew, /usr/local, and a custom
// HOMEBREW_PREFIX) and cannot false-positive on a manual install into
// /usr/local/bin, which a bare prefix match would.
const brewCaskDirName = "Caskroom"

// BrewManagedBinary reports whether the running binary was installed by a
// Homebrew cask, returning the resolved path when it was. Homebrew owns that
// path, so self-update must defer to `brew upgrade` rather than write over it.
func BrewManagedBinary() (string, bool) {
	if runtime.GOOS == "windows" {
		return "", false
	}
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		resolved = exe
	}
	for _, seg := range strings.Split(resolved, string(filepath.Separator)) {
		if seg == brewCaskDirName {
			return resolved, true
		}
	}
	return "", false
}

// EnsureHarnessHome creates ~/.harness with 0700 permissions if it does not exist.
// Returns an error if the directory cannot be created or if the path exists but is not a directory.
func EnsureHarnessHome() error {
	dir := GetHarnessHomeDir()
	info, err := os.Stat(dir)
	if os.IsNotExist(err) {
		if mkErr := os.MkdirAll(dir, 0700); mkErr != nil {
			return fmt.Errorf("cannot create harness home directory %q: %w", dir, mkErr)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("cannot access harness home directory %q: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("harness home path %q exists but is not a directory", dir)
	}
	return nil
}

func GetHomeDir() string {
	homeVar, err := os.UserHomeDir()
	if err != nil {
		return "/"
	}
	return homeVar
}

func ExpandHomeDir(pathStr string) string {
	if pathStr != "~" && !strings.HasPrefix(pathStr, "~/") && (!strings.HasPrefix(pathStr, `~\`) || runtime.GOOS != "windows") {
		return filepath.Clean(pathStr)
	}
	homeDir := GetHomeDir()
	if pathStr == "~" {
		return homeDir
	}
	return filepath.Clean(filepath.Join(homeDir, pathStr[2:]))
}
