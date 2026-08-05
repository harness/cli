// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"fmt"
	"io"

	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/format"
	"github.com/harness/cli/pkg/spec"
)

const corePrefix = "core"

// registerCoreFormatters registers all built-in "core:*" text formatters and
// list transform fns. These are available to any module via
// text_formatter: core:<name> / list_transform_fn: core:<name>.
func (r *Registry) registerCoreFormatters() {
	r.RegisterTextFormatter("core:metadata-text", formatMetadataText)
	r.RegisterListTransformFn("core:columns-rows", columnsRowsListTransform)
}

// formatMetadataText renders a metadata response ([]any of {key, value, type} objects)
// as a simple key=value list.
func formatMetadataText(w io.Writer, d cmdctx.DataAccessor) error {
	items := d.GetSlice("it")
	if len(items) == 0 {
		fmt.Fprintln(w, "(no metadata)")
		return nil
	}
	rows := make([]format.LabeledValue, 0, len(items))
	for _, raw := range items {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		key, _ := m["key"].(string)
		val, _ := m["value"].(string)
		rows = append(rows, format.LabeledValue{Label: key, Value: val})
	}
	format.WriteLabeledValues(w, rows)
	return nil
}

// columnsRowsListTransform converts a {columns, rows} query result (HQL and similar)
// into list-rendering inputs. Use with list_transform_fn: core:columns-rows.
func columnsRowsListTransform(ctx *cmdctx.Ctx, data any) ([]any, []spec.FieldDef, cmdctx.PageMeta, error) {
	rows, fields, ok := format.ExpandColumnsRows(data)
	if !ok {
		return nil, nil, cmdctx.PageMeta{}, fmt.Errorf("response is not a columns/rows result")
	}
	var meta cmdctx.PageMeta
	if m, ok := data.(map[string]any); ok {
		if truncated, _ := m["truncated"].(bool); truncated {
			meta.Notice = "(truncated)"
		}
	}
	return rows, fields, meta, nil
}
