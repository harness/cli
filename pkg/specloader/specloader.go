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

type specTypeOnly struct {
	ModuleType string `yaml:"module_type"`
}

// isEmbeddedPluginSpec reports whether an embedded spec declares module_type:
// plugin. Such specs are not builtins — the host loads them dynamically from the
// home spec dir (via a binary_path), so the main binary must not auto-register
// them from the embedded FS.
func isEmbeddedPluginSpec(data []byte) bool {
	var t specTypeOnly
	_ = yaml.Unmarshal(data, &t)
	return t.ModuleType == "plugin"
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
// Embedded specs that declare module_type: plugin (e.g. har) are NOT loaded here
// — they are plugins, not builtins, and reach the host only through the home
// spec dir (with a binary_path to dispatch to). This lets a dev-installed plugin
// spec in ~/.harness/spec drive the real dynamic path instead of being masked by
// an embedded copy.
func LoadSpecs(reg *registry.Registry) error {
	isHarnessUser := config.AnyProfileMatchesDomain("harness.io")
	for _, name := range spec.Files() {
		data, err := spec.Read(name)
		if err != nil {
			return fmt.Errorf("spec: read %s: %w", name, err)
		}
		if isEmbeddedPluginSpec(data) {
			continue
		}
		if err := loadSpecData(reg, name, data, isHarnessUser, false); err != nil {
			return err
		}
	}
	return LoadHomeSpecs(reg, isHarnessUser)
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

// LoadSpec loads a single embedded spec file (e.g. "har.spec.yaml") into reg.
// isHarnessUser gates modules marked harness_internal: true.
func LoadSpec(reg *registry.Registry, name string, isHarnessUser bool) error {
	data, err := spec.Read(name)
	if err != nil {
		return fmt.Errorf("spec: read %s: %w", name, err)
	}
	return loadSpecData(reg, name, data, isHarnessUser, false)
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
	var f specFile
	if reg.StrictYAML {
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if err := dec.Decode(&f); err != nil {
			return specParseError(name, data, err)
		}
	} else {
		if err := yaml.Unmarshal(data, &f); err != nil {
			return specParseError(name, data, err)
		}
	}
	if f.SpecVersion < MinSpecVersion || f.SpecVersion > MaxSpecVersion {
		return fmt.Errorf("spec: %s: spec_version %d out of supported range [%d, %d]", name, f.SpecVersion, MinSpecVersion, MaxSpecVersion)
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
