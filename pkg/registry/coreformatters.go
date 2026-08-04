// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"fmt"
	"io"

	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/format"
)

const corePrefix = "core"

// registerCoreFormatters registers all built-in "core:*" text formatters.
// These are available to any module via text_formatter: core:<name>.
func (r *Registry) registerCoreFormatters() {
	r.RegisterTextFormatter("core:metadata-text", formatMetadataText)
	r.RegisterTextFormatter("core:columns-rows", formatColumnsRows)
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

// formatColumnsRows renders a {columns, rows} query result (HQL and similar) as a table.
// Use with text_formatter: core:columns-rows. For --format table|csv|tsv, FormatSingleOutput
// expands the same shape automatically via format.ExpandColumnsRows.
func formatColumnsRows(w io.Writer, d cmdctx.DataAccessor) error {
	ok, err := format.WriteColumnsRows(w, d.GetData(), false)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("response is not a columns/rows result")
	}
	return nil
}
