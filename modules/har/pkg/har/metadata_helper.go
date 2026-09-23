// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"fmt"
	"os"
	"strings"

	"github.com/harness/cli/v3/pkg/client"
	"github.com/harness/cli/v3/pkg/cmdctx"
)

// parsePushMetadataString parses the --metadata flag value, a comma-separated
// list of colon-separated key:value pairs, e.g. "key:value,key2:value2".
func parsePushMetadataString(s string) ([]map[string]string, error) {
	var items []map[string]string
	for _, pair := range strings.Split(s, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		key, value, ok := strings.Cut(pair, ":")
		if !ok {
			return nil, fmt.Errorf("invalid metadata pair %q: expected key:value", pair)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			return nil, fmt.Errorf("invalid metadata pair %q: key and value must both be non-empty", pair)
		}
		items = append(items, map[string]string{"key": key, "value": value})
	}
	return items, nil
}

// applyPostPushMetadata attaches key-value metadata to an artifact (or a
// specific artifact version) right after a successful push. metadataStr uses
// "key:value,key2:value2" format. A no-op when metadataStr or pkg is empty.
// Failures are printed as warnings and never fail the push.
func applyPostPushMetadata(ctx *cmdctx.Ctx, metadataStr, registry, pkg, version string) {
	if metadataStr == "" {
		return
	}
	if pkg == "" {
		fmt.Fprintln(os.Stderr, "Warning: skipping --metadata publish; package name is not resolvable for this push")
		return
	}

	items, err := parsePushMetadataString(metadataStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to parse --metadata: %v\n", err)
		return
	}
	if len(items) == 0 {
		return
	}

	body := map[string]any{
		"registryIdentifier": registry,
		"package":            pkg,
		"metadata":           items,
	}
	if version != "" {
		body["version"] = version
	}

	c := client.New(ctx)
	if _, _, err := c.Post("/har/api/v2/metadata", map[string]string{}, body); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to publish metadata for %s/%s: %v\n", registry, pkg, err)
		return
	}
	fmt.Fprintf(os.Stderr, "Published %d metadata key(s) to %s/%s\n", len(items), registry, pkg)
}
