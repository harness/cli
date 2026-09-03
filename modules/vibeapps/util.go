// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package vibeapps

import "encoding/json"

// sentinelSpaceID is the single hardcoded default space UUID the vibe-orchestrator
// API uses server-side today (no multi-space concept exists yet).
const sentinelSpaceID = "00000000-0000-0000-0000-000000000001"

// apiPrefix is the vibe-orchestrator service's mount point ahead of its own
// /api/v1 routes, matching the paths used in pkg/spec/vibeapps.spec.yaml.
const apiPrefix = "/vibe-orchestrator"

// decodeInto re-marshals a generically-decoded API response (map[string]any, as
// returned by pkg/client) into a concrete struct, so callers get typed field access.
func decodeInto(raw any, out any) error {
	b, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}
