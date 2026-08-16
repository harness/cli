// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package vibe

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/harness/cli/pkg/auth"
	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/exprenv"
	"github.com/harness/cli/pkg/format"
	"github.com/harness/cli/pkg/spec"
)

var vibeAppListColumns = []string{"id", "name", "slug", "status", "previewUrl", "productionUrl"}

var vibeAppFields = []spec.FieldDef{
	{ID: "id", Expr: "it.id"},
	{ID: "name", Expr: "it.name"},
	{ID: "slug", Expr: "it.slug"},
	{ID: "status", Expr: "it.status"},
	{ID: "previewUrl", Label: "Preview URL", Expr: "it.previewUrl", WidthMax: 40},
	{ID: "productionUrl", Label: "Production URL", Expr: "it.productionUrl", WidthMax: 40},
}

func resolveAuth(ctx *cmdctx.Ctx) *auth.ResolvedAuth {
	if ctx.Auth != nil {
		return ctx.Auth
	}
	loaded, err := auth.Load("")
	if err == nil && loaded != nil {
		return loaded
	}
	return nil
}

func newAPI(ctx *cmdctx.Ctx) *vibeAPI {
	return newVibeAPI(resolveAuth(ctx))
}

func writeList(ctx *cmdctx.Ctx, rows []map[string]any, fields []spec.FieldDef, columns []string) error {
	items := make([]any, len(rows))
	for i, row := range rows {
		items[i] = row
	}
	formatName := cmdctx.GetString(ctx.FlagValues, "format")
	if formatName == "table" || (formatName == "" && ctx.IsPty && len(fields) > 0) {
		fieldByID := map[string]spec.FieldDef{}
		for _, f := range fields {
			fieldByID[f.ID] = f
		}
		var cols []spec.TableColumn
		for _, id := range columns {
			f, ok := fieldByID[id]
			if !ok {
				continue
			}
			header := f.Label
			if header == "" {
				header = f.ID
			}
			cols = append(cols, spec.TableColumn{
				Header:    header,
				Expr:      f.Expr,
				Align:     f.Align,
				FieldType: f.FieldType,
				WidthMax:  f.WidthMax,
			})
		}
		tspec := &spec.TableSpec{Columns: cols}
		return format.FormatArrayOutput(ctx.FormatFlags, ctx.IsPty, items, "it", tspec, fields, exprenv.Make(ctx), nil)
	}
	return writeJSON(items)
}

func writeJSON(data any) error {
	out, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(os.Stdout, "%s\n", out)
	return err
}

func writeGet(ctx *cmdctx.Ctx, data any) error {
	formatName := cmdctx.GetString(ctx.FlagValues, "format")
	if formatName == "" || formatName == "json" {
		return writeJSON(data)
	}
	return format.FormatSingleOutput(ctx.FormatFlags, ctx.IsPty, data, "it", "", nil, nil, exprenv.Make(ctx))
}
