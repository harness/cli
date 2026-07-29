// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package mgmt

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/hbase"
	"github.com/harness/cli/pkg/hlog"
	"github.com/harness/cli/pkg/plugin"
	"github.com/harness/cli/pkg/specloader"
)

// InstallPluginHandler installs a plugin from a local binary path. This is the
// simplest install form — a direct binary, no tarball fetch. It runs the shared
// install routine: identity gate (--version --json) → capture grammar (--spec) →
// write <name>.spec.yaml with the host-owned provenance block. Core is the only
// writer of spec files.
func InstallPluginHandler(ctx *cmdctx.Ctx) error {
	ref := ctx.Id
	if ref == "" {
		return fmt.Errorf("install plugin requires a binary path: harness install plugin <binary-path>")
	}

	// Only the local-binary form is supported today; URL / name resolution
	// funnel here later (they all end at a binary on disk).
	binPath, err := filepath.Abs(hbase.ExpandHomeDir(ref))
	if err != nil {
		return fmt.Errorf("resolving %q: %w", ref, err)
	}
	info, err := os.Stat(binPath)
	if err != nil {
		return fmt.Errorf("no binary at %q: %w", binPath, err)
	}
	if info.IsDir() {
		return fmt.Errorf("%q is a directory, not a plugin binary", binPath)
	}

	// Identity gate: prove it's a cooperating harness plugin before we trust its
	// name or grammar.
	id, err := plugin.QueryIdentity(binPath)
	if err != nil {
		return err
	}
	hlog.Info("plugin identity verified", "name", id.Name, "version", id.Version)

	// Capture the plugin's self-contained grammar.
	grammar, err := exec.Command(binPath, "--spec").Output()
	if err != nil {
		return fmt.Errorf("plugin %q failed to emit its spec (--spec): %w", id.Name, err)
	}

	// Write the spec = grammar + host-owned provenance into ~/.harness/spec.
	specDir := specloader.HomeSpecDir()
	if err := os.MkdirAll(specDir, 0700); err != nil {
		return fmt.Errorf("creating spec dir %q: %w", specDir, err)
	}
	if err := os.Chmod(specDir, 0700); err != nil {
		return fmt.Errorf("securing spec dir %q: %w", specDir, err)
	}

	// The spec file is one YAML document: the plugin's grammar plus the
	// host-owned provenance fields. Decode the grammar, add provenance to the
	// same mapping, and encode the whole thing once.
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
	setMapField(root, "source", "local:"+binPath)
	setMapField(root, "generated_at", time.Now().UTC().Format(time.RFC3339))

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return fmt.Errorf("encoding spec: %w", err)
	}

	specPath := filepath.Join(specDir, id.Name+".spec.yaml")
	if err := os.WriteFile(specPath, out, 0600); err != nil {
		return fmt.Errorf("writing spec %q: %w", specPath, err)
	}
	hlog.Debug("wrote plugin spec", "path", specPath, "binary", binPath)

	fmt.Printf("Installed plugin %q %s to %s\n", id.Name, id.Version, hbase.GetHarnessHomeDir())
	return nil
}

// setMapField sets key to a scalar value on a YAML mapping node, replacing an
// existing entry or appending a new key/value pair. This lets provenance be
// written as part of the spec document rather than spliced onto its bytes.
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
