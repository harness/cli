// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package rt

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/spec"
)

const usageReportRowsID = "usage_report_rows"

// The route answers with a bare matrix, not objects; reshaping into rows is what buys --format.
// The trailing TOTAL row is kept — it is the account figure the report exists to give.
func usageReportRows(_ *cmdctx.Ctx, data any) ([]any, []spec.FieldDef, cmdctx.PageMeta, error) {
	matrix, ok := data.([]any)
	if !ok {
		return nil, nil, cmdctx.PageMeta{}, fmt.Errorf("the usage report came back as %T, not the expected matrix of rows", data)
	}
	// The service always writes a header, so no rows means an empty report, not an unreadable shape.
	if len(matrix) == 0 {
		return nil, nil, cmdctx.PageMeta{}, nil
	}

	ids, fields := usageReportFields(matrix[0])
	rows := make([]any, 0, len(matrix)-1)
	for _, raw := range matrix[1:] {
		cells, _ := raw.([]any)
		row := make(map[string]any, len(ids))
		for i, id := range ids {
			// A short row leaves trailing columns unset: "not reported" rather than zero.
			if i < len(cells) {
				row[id] = cells[i]
			}
		}
		rows = append(rows, row)
	}
	return rows, fields, cmdctx.PageMeta{}, nil
}

// Keeps the service's own heading as the label so exported CSV matches the API,
// while --columns matches on the snake_case id used everywhere else in the CLI.
func usageReportFields(header any) ([]string, []spec.FieldDef) {
	cells, _ := header.([]any)
	ids := make([]string, 0, len(cells))
	fields := make([]spec.FieldDef, 0, len(cells))
	used := make(map[string]bool, len(cells))

	for i, cell := range cells {
		label, _ := cell.(string)
		id := snakeCase(label)
		if id == "" {
			id = fmt.Sprintf("col_%d", i)
		}
		if used[id] {
			base := id
			for suffix := 2; ; suffix++ {
				id = fmt.Sprintf("%s_%d", base, suffix)
				if !used[id] {
					break
				}
			}
		}
		used[id] = true
		ids = append(ids, id)
		fields = append(fields, spec.FieldDef{
			ID:    id,
			Label: label,
			Expr:  fmt.Sprintf("it[%q]", id),
		})
	}
	return ids, fields
}

// ServiceID becomes service_id, VUSeconds becomes vu_seconds: runs of capitals stay one word.
func snakeCase(s string) string {
	var b strings.Builder
	runes := []rune(s)
	for i, r := range runes {
		switch {
		case r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r):
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "_"):
			b.WriteByte('_')
			continue
		default:
			continue
		}
		// A capital opens a word after a lowercase or digit, or when it ends a run before one.
		if unicode.IsUpper(r) && i > 0 && b.Len() > 0 && !strings.HasSuffix(b.String(), "_") {
			previous := runes[i-1]
			startsWord := unicode.IsLower(previous) || unicode.IsDigit(previous)
			endsAcronym := unicode.IsUpper(previous) && i+1 < len(runes) &&
				unicode.IsLower(runes[i+1]) && !pluralSuffix(runes, i+1)
			if startsWord || endsAcronym {
				b.WriteByte('_')
			}
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return strings.Trim(b.String(), "_")
}

// A lone "s" closing a capital run, as in VUs — without this it splits into v_us.
func pluralSuffix(runes []rune, i int) bool {
	return runes[i] == 's' && (i+1 == len(runes) || !unicode.IsLower(runes[i+1]))
}
