// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

// Package fme implements field-mutation handlers specific to Harness FME
// (Feature Management & Experimentation). Commands are declared in fme.spec.yaml.
package fme

import "github.com/harness/cli/v3/pkg/registry"

// ModuleInit registers FME-specific field types. Owners and tags need custom
// handlers because core's built-in "tags" type is a string-keyed map, not a
// fit for FME's named-object collections (see docs/mutation.md). metric_refs
// needs one too: keyMetrics/supportingMetrics read as [{id,name}] but write
// as plain metric id strings, which core's built-in "set" type doesn't handle.
func ModuleInit(reg registry.ModuleRegistrar) {
	reg.RegisterFieldType("owners", ownersFieldType)
	reg.RegisterFieldType("tags", tagsFieldType)
	reg.RegisterFieldType("flag_sets", flagSetsFieldType)
	reg.RegisterFieldType("metric_refs", metricRefsFieldType)
}
