// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package specloader

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/harness/cli/pkg/config"
	"github.com/harness/cli/pkg/hbase"
	"github.com/harness/cli/pkg/registry"
	"github.com/harness/cli/pkg/spec"
)

// TestLoadAllEmbeddedSpecs verifies that every bundled *.spec.yaml parses
// without error and registers without conflicts. This catches duplicate nouns,
// duplicate commands, bad YAML, and unknown fields before they reach users.
func TestLoadAllEmbeddedSpecs(t *testing.T) {
	reg := registry.New()
	if err := LoadSpecs(reg); err != nil {
		t.Fatalf("LoadSpecs failed: %v", err)
	}
}

// TestEmbeddedSpecPartition verifies that spec.Files() and spec.PluginFiles()
// partition the embedded specs by module_type. A plugin spec that leaks into
// Files() would be registered as a builtin, masking the installed plugin's
// binary_path; a builtin spec listed in PluginFiles() would never register at
// all.
func TestEmbeddedSpecPartition(t *testing.T) {
	check := func(name string, wantPlugin bool) {
		data, err := spec.Read(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		var f specFile
		if err := yaml.Unmarshal(data, &f); err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		if gotPlugin := f.ModuleType == spec.ModuleTypePlugin; gotPlugin != wantPlugin {
			if wantPlugin {
				t.Errorf("%s is in spec.PluginFiles() but declares module_type: %q — remove it from pluginSpecFiles", name, f.ModuleType)
			} else {
				t.Errorf("%s declares module_type: plugin but is not in spec.PluginFiles() — add it to pluginSpecFiles in pkg/spec/spec.go", name)
			}
		}
	}
	for _, name := range spec.Files() {
		check(name, false)
	}
	plugins := spec.PluginFiles()
	if len(plugins) == 0 {
		t.Fatal("spec.PluginFiles() is empty — expected at least har.spec.yaml")
	}
	for _, name := range plugins {
		check(name, true)
	}
}

// TestDuplicateNoun ensures that loading two specs that declare the same noun
// produces a clear error rather than a silent override.
func TestDuplicateNoun(t *testing.T) {
	reg := registry.New()
	if err := parseAndLoad(reg, "a.spec.yaml", []byte(`
spec_version: 1
nouns:
  - noun: widget
    fields:
      - id: identifier
        expr: it.id
`)); err != nil {
		t.Fatalf("loading specA: %v", err)
	}
	err := parseAndLoad(reg, "b.spec.yaml", []byte(`
spec_version: 1
nouns:
  - noun: widget
    fields:
      - id: identifier
        expr: it.id
`))
	if err == nil {
		t.Fatal("expected error for duplicate noun, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate noun") {
		t.Errorf("error should mention 'duplicate noun', got: %v", err)
	}
}

// TestStrictYAML verifies strict mode rejects unknown top-level YAML keys.
func TestStrictYAML(t *testing.T) {
	reg := registry.New()
	reg.StrictYAML = true
	err := parseAndLoad(reg, "bad.spec.yaml", []byte(`
spec_version: 1
totally_unknown_root_key: oops
nouns:
  - noun: thing
    fields: []
`))
	if err == nil {
		t.Fatal("expected error for unknown field in strict mode, got nil")
	}
}

// TestMinimalSpec confirms that a minimal valid spec loads cleanly.
func TestMinimalSpec(t *testing.T) {
	reg := registry.New()
	if err := parseAndLoad(reg, "minimal.spec.yaml", []byte(`
spec_version: 1
nouns:
  - noun: thing
    fields:
      - id: identifier
        expr: it.id
commands:
  - command: list thing
    verb: list
    noun: thing
    short: List things
    handler_type: endpoint
    endpoint:
      path: /api/things
      items_expr: it
      paging:
        paging_strategy: flat_list
`)); err != nil {
		t.Fatalf("minimal spec failed: %v", err)
	}
}

// TestIsModuleEnabled covers the priority sequence a module_type: hidden
// module is resolved through: HARNESS_CLI_ENABLED_MODULES (hard override,
// including set-and-empty), then HARNESS_ENABLE_BETA_MODULES, then the
// harness.io auto-detect, then the config file.
func TestIsModuleEnabled(t *testing.T) {
	cfgWith := func(names ...string) *config.Config { return &config.Config{EnabledModules: names} }

	tests := []struct {
		name               string
		module             string
		cfg                *config.Config
		harnessDomainMatch bool
		enabledModulesEnv  *string // nil = unset
		enableBetaEnv      string
		want               bool
	}{
		{
			name:   "hidden module with nothing enabling it is disabled",
			module: "autonomous_work",
			cfg:    cfgWith(),
			want:   false,
		},
		{
			name:              "HARNESS_CLI_ENABLED_MODULES allowlist hit",
			module:            "autonomous_work",
			cfg:               cfgWith(),
			enabledModulesEnv: strPtr("foo, autonomous_work, bar"),
			want:              true,
		},
		{
			name:               "HARNESS_CLI_ENABLED_MODULES set-and-empty disables everything, even with domain match and config entry",
			module:             "autonomous_work",
			cfg:                cfgWith("autonomous_work"),
			harnessDomainMatch: true,
			enabledModulesEnv:  strPtr(""),
			want:               false,
		},
		{
			name:          "HARNESS_ENABLE_BETA_MODULES=1 enables everything",
			module:        "autonomous_work",
			cfg:           cfgWith(),
			enableBetaEnv: "1",
			want:          true,
		},
		{
			name:               "HARNESS_ENABLE_BETA_MODULES=0 suppresses auto-detect but still honors config",
			module:             "autonomous_work",
			cfg:                cfgWith("autonomous_work"),
			harnessDomainMatch: true,
			enableBetaEnv:      "0",
			want:               true,
		},
		{
			name:               "HARNESS_ENABLE_BETA_MODULES=0 with no config entry stays disabled despite domain match",
			module:             "autonomous_work",
			cfg:                cfgWith(),
			harnessDomainMatch: true,
			enableBetaEnv:      "0",
			want:               false,
		},
		{
			name:               "unset + harness.io domain match auto-enables",
			module:             "autonomous_work",
			cfg:                cfgWith(),
			harnessDomainMatch: true,
			want:               true,
		},
		{
			name:   "plain config-file enablement with no env vars set",
			module: "autonomous_work",
			cfg:    cfgWith("autonomous_work"),
			want:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setEnvOrUnset(t, hbase.EnvEnabledModules, tc.enabledModulesEnv)
			t.Setenv(hbase.EnvEnableBetaModules, tc.enableBetaEnv)

			got := isModuleEnabled(tc.module, tc.cfg, tc.harnessDomainMatch)
			if got != tc.want {
				t.Errorf("isModuleEnabled(%q) = %v, want %v", tc.module, got, tc.want)
			}
		})
	}
}

func strPtr(s string) *string { return &s }

// setEnvOrUnset sets key to *val, or ensures it's unset when val is nil —
// distinct from setting it to "", since isModuleEnabled uses os.LookupEnv to
// tell "unset" from "set to empty". Restores the original value on cleanup.
func setEnvOrUnset(t *testing.T, key string, val *string) {
	orig, hadOrig := os.LookupEnv(key)
	t.Cleanup(func() {
		if hadOrig {
			os.Setenv(key, orig)
		} else {
			os.Unsetenv(key)
		}
	})
	if val == nil {
		os.Unsetenv(key)
		return
	}
	os.Setenv(key, *val)
}

// TestLoadSpecData_HiddenModule verifies that a module_type: hidden module is
// fully absent from the registry when not enabled (nouns/commands unregistered,
// no ModuleMeta) except for the RecordHiddenModule stub that lets `install
// module` find it by name, and that it registers normally once enabled.
func TestLoadSpecData_HiddenModule(t *testing.T) {
	specYAML := []byte(`
spec_version: 1
module_type: hidden
module_desc: a hidden module
nouns:
  - noun: widget
    fields:
      - id: identifier
        expr: it.id
`)

	t.Run("disabled: absent from registry, recorded as hidden", func(t *testing.T) {
		reg := registry.New()
		if err := loadSpecData(reg, "widget.spec.yaml", specYAML, false, false); err != nil {
			t.Fatalf("loadSpecData: %v", err)
		}
		if reg.HasModule("widget") {
			t.Error("disabled hidden module should not be registered")
		}
		if reg.GetNoun("widget") != nil {
			t.Error("disabled hidden module's noun should not be registered")
		}
		m := reg.GetHiddenModule("widget")
		if m == nil {
			t.Fatal("GetHiddenModule(\"widget\") = nil, want a stub recorded regardless of enablement")
		}
		if m.Desc != "a hidden module" {
			t.Errorf("GetHiddenModule(\"widget\").Desc = %q, want %q", m.Desc, "a hidden module")
		}
	})

	t.Run("enabled: registers normally", func(t *testing.T) {
		reg := registry.New()
		if err := loadSpecData(reg, "widget.spec.yaml", specYAML, true, false); err != nil {
			t.Fatalf("loadSpecData: %v", err)
		}
		if !reg.HasModule("widget") {
			t.Error("enabled hidden module should be registered")
		}
		if reg.GetNoun("widget") == nil {
			t.Error("enabled hidden module's noun should be registered")
		}
	})
}

// parseAndLoad mirrors LoadSpec but accepts raw bytes instead of reading from
// embed.FS, allowing unit tests to exercise the parse-and-register path.
func parseAndLoad(reg *registry.Registry, name string, data []byte) error {
	var f specFile
	if reg.StrictYAML {
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if err := dec.Decode(&f); err != nil {
			return specParseError(name, data, err)
		}
	} else if err := yaml.Unmarshal(data, &f); err != nil {
		return specParseError(name, data, err)
	}
	if f.SpecVersion < MinSpecVersion || f.SpecVersion > MaxSpecVersion {
		return specParseError(name, data, nil)
	}
	for i, nd := range f.Nouns {
		if err := reg.RegisterNoun(nd); err != nil {
			return wrapSpecErr(name, "noun", i, err)
		}
	}
	mod := reg.Module(strings.TrimSuffix(name, ".spec.yaml"))
	for i, cmd := range f.Commands {
		if cmd == nil {
			continue
		}
		cmd.SpecFile = name
		if err := mod.Register(cmd); err != nil {
			return wrapSpecErr(name, "command", i, err)
		}
	}
	return nil
}

func wrapSpecErr(name, kind string, i int, err error) error {
	return &specErr{name: name, kind: kind, index: i, err: err}
}

type specErr struct {
	name, kind string
	index      int
	err        error
}

func (e *specErr) Error() string {
	return "spec: " + e.name + " " + e.kind + "[" + strings.Repeat("x", e.index) + "]: " + e.err.Error()
}

func (e *specErr) Unwrap() error { return e.err }

// satisfy interface — test uses strings.Contains, not errors.As
var _ error = (*specErr)(nil)

// Ensure specFile is accessible (it's defined in specloader.go, same package).
var _ = specFile{}
var _ = spec.NounDef{}
