// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package specloader

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/harness/cli/pkg/config"
	"github.com/harness/cli/pkg/hbase"
	"github.com/harness/cli/pkg/hlog"
	"github.com/harness/cli/pkg/registry"
	"github.com/harness/cli/pkg/spec"
)

const (
	MinSpecVersion = 1
	MaxSpecVersion = 1

	// maxHomeSpecSize bounds how large a single ~/.harness/spec file may be.
	// Real specs (even core's) are tens of KB; this just keeps a corrupted or
	// hostile file from reaching the YAML parser.
	maxHomeSpecSize = 1 << 20 // 1 MiB
)

type specVersionOnly struct {
	SpecVersion int `yaml:"spec_version"`
}

type specFile struct {
	SpecVersion     int                 `yaml:"spec_version"`
	ModuleType      string              `yaml:"module_type"`
	ModuleDesc      string              `yaml:"module_desc"`
	ModuleCore      bool                `yaml:"module_core"`
	HelpText        string              `yaml:"help_text"`
	HarnessInternal bool                `yaml:"harness_internal,omitempty"`
	Nouns           []spec.NounDef      `yaml:"nouns"`
	Commands        []*spec.CommandSpec `yaml:"commands"`
	// Host-owned provenance, present only in ~/.harness/spec plugin specs.
	Version     string `yaml:"version,omitempty"`
	BinaryPath  string `yaml:"binary_path,omitempty"`
	Source      string `yaml:"source,omitempty"`
	InstalledAt string `yaml:"installed_at,omitempty"`
}

// specParseError wraps a YAML parse failure, enriching it with the spec_version
// when the full parse fails due to a schema mismatch.
func specParseError(name string, data []byte, parseErr error) error {
	var v specVersionOnly
	if yaml.Unmarshal(data, &v) == nil && (v.SpecVersion < MinSpecVersion || v.SpecVersion > MaxSpecVersion) {
		return fmt.Errorf("spec: %s: spec_version %d out of supported range [%d, %d]", name, v.SpecVersion, MinSpecVersion, MaxSpecVersion)
	}
	return fmt.Errorf("spec: parse %s: %w", name, parseErr)
}

// LoadSpecs loads all embedded builtin spec files into reg, then any
// dynamically installed plugin specs from ~/.harness/spec. Embedded specs always
// win: a home spec whose module name is already registered is skipped with a
// warning.
//
// Embedded specs for plugin modules (spec.PluginFiles(), e.g. har) are not
// registered here — they are plugins, not builtins, and reach the host only
// through the home spec dir (with a binary_path to dispatch to). This lets a
// dev-installed plugin spec in ~/.harness/spec drive the real dynamic path
// instead of being masked by an embedded copy. Only their noun ownership is
// recorded (best-effort, for "plugin not installed" error messages).
func LoadSpecs(reg *registry.Registry) error {
	isHarnessUser := os.Getenv("HARNESS_ENABLE_BETA_MODULES") == "1" ||
		config.AnyProfileMatchesDomain("harness.io")
	for _, name := range spec.Files() {
		data, err := spec.Read(name)
		if err != nil {
			return fmt.Errorf("spec: read %s: %w", name, err)
		}
		if err := loadSpecData(reg, name, data, isHarnessUser, false); err != nil {
			return err
		}
	}
	for _, name := range spec.PluginFiles() {
		data, err := spec.Read(name)
		if err != nil {
			return fmt.Errorf("spec: read %s: %w", name, err)
		}
		if err := recordPluginNouns(reg, name, data, isHarnessUser); err != nil {
			return err
		}
	}
	return LoadHomeSpecs(reg, isHarnessUser)
}

// recordPluginNouns records which nouns an embedded plugin spec owns, without
// registering any of its commands. A harness_internal plugin spec records
// nothing for non-Harness users, matching how builtin specs are gated.
func recordPluginNouns(reg *registry.Registry, name string, data []byte, isHarnessUser bool) error {
	f, err := parseSpecFile(reg, name, data)
	if err != nil {
		return err
	}
	if f.HarnessInternal && !isHarnessUser {
		return nil
	}
	reg.RecordPluginOwnedNouns(moduleNameFromFile(name), f.Nouns)
	return nil
}

// LoadHomeSpecs loads plugin spec files from ~/.harness/spec (the sole source
// of truth for dynamically-installed plugins). A missing directory is not an
// error. Home specs whose module name collides with an already-registered
// (embedded) module are skipped with a stderr warning — embedded always wins.
func LoadHomeSpecs(reg *registry.Registry, isHarnessUser bool) error {
	dir := HomeSpecDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("spec: read home spec dir %s: %w", dir, err)
	}
	// Deterministic order regardless of filesystem enumeration.
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		// Type() reflects the directory entry itself (not a followed stat), so
		// this rejects symlinks, pipes, devices, sockets — anything but a plain
		// file — without an extra syscall.
		if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), ".spec.yaml") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		module := moduleNameFromFile(name)
		if reg.HasModule(module) {
			hlog.Warn("skipping plugin spec: module already registered (embedded wins)", "module", module, "file", name)
			continue
		}
		path := filepath.Join(dir, name)
		// Lstat, not Stat: don't follow a symlink that got swapped in after the
		// directory listing above already ruled one out.
		info, statErr := os.Lstat(path)
		if statErr != nil {
			hlog.Warn("skipping unreadable plugin spec", "file", path, "err", statErr)
			continue
		}
		if !info.Mode().IsRegular() {
			hlog.Warn("skipping non-regular plugin spec", "file", path)
			continue
		}
		if info.Size() == 0 {
			hlog.Warn("skipping empty plugin spec", "file", path)
			continue
		}
		if info.Size() > maxHomeSpecSize {
			hlog.Warn("skipping oversized plugin spec", "file", path, "size", info.Size(), "max", maxHomeSpecSize)
			continue
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			hlog.Warn("skipping unreadable plugin spec", "file", path, "err", readErr)
			continue
		}
		if err := loadSpecData(reg, name, data, isHarnessUser, true); err != nil {
			// A single bad plugin spec must not take down the whole CLI.
			hlog.Warn("skipping invalid plugin spec", "file", path, "err", err)
			continue
		}
	}
	return nil
}

// HomeSpecDir returns the directory that holds dynamically-installed plugin
// specs: ~/.harness/spec (respecting HARNESS_CLI_HOME).
func HomeSpecDir() string {
	return filepath.Join(hbase.GetHarnessHomeDir(), "spec")
}

func moduleNameFromFile(name string) string {
	return strings.SplitN(filepath.Base(name), ".", 2)[0]
}

// ReadSpecFile returns the raw bytes of the spec file for a module (e.g. "har" → har.spec.yaml).
func ReadSpecFile(moduleName string) ([]byte, error) {
	return spec.Read(moduleName + ".spec.yaml")
}

// Returns the raw bytes it read so a plugin main() can also hand them to
// rootcmd.SetupAndExecutePluginRootCmd for its own --spec dump, without a
// second read of the same file.
func LoadSpec(reg *registry.Registry, name string, isHarnessUser bool) ([]byte, error) {
	data, err := spec.Read(name)
	if err != nil {
		return nil, fmt.Errorf("spec: read %s: %w", name, err)
	}
	if err := loadSpecData(reg, name, data, isHarnessUser, false); err != nil {
		return nil, err
	}
	return data, nil
}

// LoadSpecBytes is LoadSpec's counterpart for a plugin whose spec file lives in
// its own repo rather than core's pkg/spec.
func LoadSpecBytes(reg *registry.Registry, name string, data []byte, isHarnessUser bool) ([]byte, error) {
	if err := loadSpecData(reg, name, data, isHarnessUser, false); err != nil {
		return nil, err
	}
	return data, nil
}

// parseSpecFile unmarshals spec bytes and checks the spec version. name is used
// only for error messages.
func parseSpecFile(reg *registry.Registry, name string, data []byte) (*specFile, error) {
	var f specFile
	if reg.StrictYAML {
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if err := dec.Decode(&f); err != nil {
			return nil, specParseError(name, data, err)
		}
	} else {
		if err := yaml.Unmarshal(data, &f); err != nil {
			return nil, specParseError(name, data, err)
		}
	}
	if f.SpecVersion < MinSpecVersion || f.SpecVersion > MaxSpecVersion {
		return nil, fmt.Errorf("spec: %s: spec_version %d out of supported range [%d, %d]", name, f.SpecVersion, MinSpecVersion, MaxSpecVersion)
	}
	return &f, nil
}

// loadSpecData parses spec bytes and registers the module, nouns, and commands.
// name is used only for module-name derivation and error messages; the bytes
// may come from the embedded FS or from a ~/.harness/spec file.
//
// fromSpecDir marks an untrusted plugin spec loaded from ~/.harness/spec (vs. a
// trusted embedded/builtin spec). For those specs we:
//   - reject top-level fields plugins may not set (e.g. module_core), and
//   - validate the whole spec against the already-loaded registry before any
//     mutation, so a spec that would collide with a builtin noun/command is
//     rejected whole rather than leaking a partial registration.
//
// Embedded specs pass fromSpecDir=false: they are load-order-first and validated
// by check:specs, so a duplicate there is a build bug we surface loudly.
func loadSpecData(reg *registry.Registry, name string, data []byte, isHarnessUser, fromSpecDir bool) error {
	module := moduleNameFromFile(name)
	f, err := parseSpecFile(reg, name, data)
	if err != nil {
		return err
	}
	if f.HarnessInternal && !isHarnessUser {
		return nil
	}
	if fromSpecDir {
		// Top-level fields an installed plugin is not allowed to declare.
		// module_core would hide the module from `list module` and mark it a
		// CLI-internal namespace — reserved for builtins. harness_internal is
		// deliberately still permitted for plugins.
		if f.ModuleCore {
			return fmt.Errorf("plugin spec %q may not set module_core", name)
		}
		if err := reg.CheckNoConflicts(f.Nouns, f.Commands); err != nil {
			return err
		}
	}
	for i, nd := range f.Nouns {
		if err := reg.RegisterNoun(nd); err != nil {
			return fmt.Errorf("spec: %s noun[%d]: %w", name, i, err)
		}
	}
	nounOrder := make([]string, len(f.Nouns))
	for i, nd := range f.Nouns {
		nounOrder[i] = nd.Noun
	}
	reg.SetModuleMeta(spec.ModuleMeta{
		Name:        module,
		Type:        f.ModuleType,
		Desc:        f.ModuleDesc,
		Core:        f.ModuleCore,
		HelpText:    f.HelpText,
		NounOrder:   nounOrder,
		FromSpecDir: fromSpecDir,
		Version:     f.Version,
		BinaryPath:  f.BinaryPath,
		Source:      f.Source,
		InstalledAt: f.InstalledAt,
	})
	mod := reg.Module(module)
	for i, cmd := range f.Commands {
		if cmd == nil {
			return fmt.Errorf("spec: %s command[%d] is nil", name, i)
		}
		cmd.SpecFile = name
		if err := mod.Register(cmd); err != nil {
			return fmt.Errorf("spec: %s command[%d]: %w", name, i, err)
		}
	}
	return nil
}
