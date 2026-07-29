// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// nameRe is the hard requirement on a plugin name: lowercase alphanumeric with
// single dashes as separators only. The name becomes a filename
// (<name>.spec.yaml), a command namespace, and a registry key, so an
// unvalidated name is a path-injection vector.
var nameRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ValidateName reports whether name is a legal plugin/module name.
func ValidateName(name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("invalid plugin name %q — must match %s (lowercase alphanumeric, single dashes as separators)", name, nameRe.String())
	}
	return nil
}

// Identity is the sentinel-gated object a cooperating harness plugin emits from
// `<plugin> --identity`. harness_plugin_name is both the gate and the name: the
// harness_ prefix makes it an unforgeable sentinel, so a binary that emits this
// key is asserting it is a harness plugin.
type Identity struct {
	Name      string `json:"harness_plugin_name"`
	Version   string `json:"version"`
	BuildTime string `json:"build_time"`
}

// QueryIdentity runs `binPath --identity`, then parses and validates the
// sentinel object. It is the plugin identity gate: it fails unless the output
// is JSON with a non-empty, well-formed harness_plugin_name. version is trusted
// only after the gate passes; build_time is displayed-only and never compared.
func QueryIdentity(binPath string) (*Identity, error) {
	out, err := exec.Command(binPath, "--identity").Output()
	if err != nil {
		return nil, fmt.Errorf("%q did not respond to `--identity` — is it a harness plugin?", binPath)
	}
	var id Identity
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &id); err != nil {
		return nil, fmt.Errorf("%q is not a harness plugin: `--identity` did not emit a JSON identity object", binPath)
	}
	if id.Name == "" {
		return nil, fmt.Errorf("%q is not a harness plugin: `--version --json` output has no harness_plugin_name", binPath)
	}
	if err := ValidateName(id.Name); err != nil {
		return nil, err
	}
	if id.Version == "" {
		return nil, fmt.Errorf("plugin %q reported an empty version", id.Name)
	}
	return &id, nil
}
