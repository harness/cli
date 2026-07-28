// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

// pkgmgrRegistryInfo holds the HAR registry info detected from a client config file.
type pkgmgrRegistryInfo struct {
	RegistryURL        string
	RegistryIdentifier string
	OrgID              string
	ProjectID          string
}

// pkgmgrSavedConfig is persisted to ~/.harness/<client>-pkgmgr.json by configure commands.
type pkgmgrSavedConfig struct {
	RegistryIdentifier string `json:"registryIdentifier"`
	RegistryURL        string `json:"registryUrl"`
	OrgID              string `json:"orgId,omitempty"`
	ProjectID          string `json:"projectId,omitempty"`
}

// pkgmgrInstallResult holds the outcome of running a native package manager command.
type pkgmgrInstallResult struct {
	Status string // "SUCCESS" or "FAILURE"
	Stderr string
	Err    error
}

// pkgmgrClient is the interface each package manager client must implement.
type pkgmgrClient interface {
	// Name returns the human-readable client name (e.g. "npm").
	Name() string

	// DetectRegistry finds the HAR registry from the config file written by
	// `configure registry`. explicitRegistry overrides detection when non-empty.
	DetectRegistry(explicitRegistry string) (*pkgmgrRegistryInfo, error)

	// RunCommand executes the native tool with args and streams output live.
	RunCommand(subcommand string, args []string) (*pkgmgrInstallResult, error)

	// DetectFirewallError returns true if stderr contains a 403 / firewall pattern.
	DetectFirewallError(stderr string) bool

	// ResolveDeps returns the full dependency list for firewall evaluation.
	// Checks well-known lock files first; falls back to native tool generation.
	// The returned cleanup func (may be nil) removes any temp files created.
	ResolveDeps() ([]dependency, func(), error)
}
