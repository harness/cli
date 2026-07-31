// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
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

// BinaryPrefix is the required prefix on every plugin binary's file name. The
// convention is load-bearing, not cosmetic: it is how a plugin binary is picked
// out of a release tarball that also holds licenses, docs, and other binaries,
// and it lets the file name be cross-checked against the gated identity name.
const BinaryPrefix = "harness-"

// BinaryName is the file name a plugin named name must be shipped and installed
// under. Callers derive this from a gated identity, never from an archive entry
// or a user-supplied path, so the install destination is always predictable.
func BinaryName(name string) string {
	return BinaryPrefix + name
}

// NameFromBinary extracts the plugin name from a binary's file name, which must
// be harness-<name>[.exe]. It reports whether the file name conforms; a
// non-conforming name is not a plugin binary as far as the host is concerned.
//
// path may be a full path — only the base name is considered.
func NameFromBinary(path string) (string, bool) {
	base := filepath.Base(path)
	base = strings.TrimSuffix(base, ".exe")
	name, ok := strings.CutPrefix(base, BinaryPrefix)
	if !ok {
		return "", false
	}
	if !nameRe.MatchString(name) {
		return "", false
	}
	return name, true
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
//
// Errors name no path: binPath is often a scratch extract dir that means nothing
// to the user, so the caller supplies the ref it wants reported.
func QueryIdentity(binPath string) (*Identity, error) {
	out, err := exec.Command(binPath, "--identity").Output()
	if err != nil {
		return nil, errors.New("did not respond to `--identity` — is it a harness plugin?")
	}
	var id Identity
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &id); err != nil {
		return nil, errors.New("not a harness plugin: `--identity` did not emit a JSON identity object")
	}
	if id.Name == "" {
		return nil, errors.New("not a harness plugin: `--identity` output has no harness_plugin_name")
	}
	if err := ValidateName(id.Name); err != nil {
		return nil, err
	}
	if id.Version == "" {
		return nil, fmt.Errorf("plugin %q reported an empty version", id.Name)
	}
	return &id, nil
}
