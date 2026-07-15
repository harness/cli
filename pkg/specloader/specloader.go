// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package specloader

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/harness/cli/pkg/config"
	"github.com/harness/cli/pkg/registry"
	"github.com/harness/cli/pkg/spec"
)

const (
	MinSpecVersion = 1
	MaxSpecVersion = 1
)

type specVersionOnly struct {
	SpecVersion int `yaml:"spec_version"`
}

type specFile struct {
	SpecVersion     int                 `yaml:"spec_version"`
	ModuleType      string              `yaml:"module_type"`
	ModuleDesc      string              `yaml:"module_desc"`
	ModuleCore      bool                `yaml:"module_core"`
	ExternalBinary  string              `yaml:"external_binary"`
	HelpText        string              `yaml:"help_text"`
	HarnessInternal bool                `yaml:"harness_internal,omitempty"`
	Nouns           []spec.NounDef      `yaml:"nouns"`
	Commands        []*spec.CommandSpec `yaml:"commands"`
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

// LoadSpecs loads all embedded spec files into reg.
func LoadSpecs(reg *registry.Registry) error {
	isHarnessUser := config.AnyProfileMatchesDomain("harness.io")
	for _, name := range spec.Files() {
		if err := LoadSpec(reg, name, isHarnessUser); err != nil {
			return err
		}
	}
	return nil
}

// ReadSpecFile returns the raw bytes of the spec file for a module (e.g. "har" → har.spec.yaml).
func ReadSpecFile(moduleName string) ([]byte, error) {
	return spec.Read(moduleName + ".spec.yaml")
}

// LoadSpec loads a single spec file (e.g. "har.spec.yaml") into reg.
// isHarnessUser gates modules marked harness_internal: true.
func LoadSpec(reg *registry.Registry, name string, isHarnessUser bool) error {
	module := strings.SplitN(filepath.Base(name), ".", 2)[0]
	data, err := spec.Read(name)
	if err != nil {
		return fmt.Errorf("spec: read %s: %w", name, err)
	}
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
		Name:           module,
		Type:           f.ModuleType,
		Desc:           f.ModuleDesc,
		Core:           f.ModuleCore,
		HelpText:       f.HelpText,
		NounOrder:      nounOrder,
		ExternalBinary: f.ExternalBinary,
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
