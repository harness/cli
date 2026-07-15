// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package mgmt

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"

	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/console"
	"github.com/harness/cli/pkg/format"
	"github.com/harness/cli/pkg/hbase"
	"github.com/harness/cli/pkg/plugin"
	"github.com/harness/cli/pkg/spec"
)

func GetModuleHandler(ctx *cmdctx.Ctx) error {
	var meta *spec.ModuleMeta
	for _, m := range ctx.Resolver.GetModuleMetas() {
		if strings.EqualFold(m.Name, ctx.Id) {
			m := m
			meta = &m
			break
		}
	}
	if meta == nil {
		return fmt.Errorf("module %q not found", ctx.Id)
	}

	// collect nouns with at least one command
	nounSet := map[string]bool{}
	for _, cs := range ctx.Resolver.GetSpecsForModule(meta.Name) {
		if cs.Noun != "" {
			nounSet[cs.Noun] = true
		}
	}
	// order by spec declaration order; nouns not in the declared list fall to the end alphabetically
	var nouns []string
	seen := map[string]bool{}
	for _, n := range meta.NounOrder {
		if nounSet[n] {
			nouns = append(nouns, n)
			seen[n] = true
		}
	}
	var remainder []string
	for n := range nounSet {
		if !seen[n] {
			remainder = append(remainder, n)
		}
	}
	sort.Strings(remainder)
	nouns = append(nouns, remainder...)

	if len(nouns) == 0 {
		return nil
	}

	// render help text: use embedded text, fall back to querying the plugin binary, or plain list
	helpText := meta.HelpText
	if helpText == "" && meta.ExternalBinary != "" {
		if binPath, err := plugin.FindBinary(meta.ExternalBinary); err == nil {
			helpText = plugin.QueryModuleHelp(binPath)
		}
	}
	if helpText != "" {
		nounBlock := RenderNounBlock(meta.Name, nouns, ctx.Resolver)
		fmt.Print(strings.ReplaceAll(helpText, "{{nouns}}", nounBlock))
		fmt.Println()
	} else {
		fmt.Printf("Module: %s — %s\n\n", meta.Name, meta.Desc)
		for _, n := range nouns {
			nd := ctx.Resolver.GetNoun(n)
			if nd != nil && nd.ShortDesc != "" {
				fmt.Printf("  %s — %s\n", n, nd.ShortDesc)
			} else {
				fmt.Printf("  %s\n", n)
			}
		}
		fmt.Println()
	}

	showMatrix := cmdctx.GetBool(ctx.FlagValues, "matrix")
	if showMatrix {
		renderMatrix(ctx, meta, nouns)
	}

	return nil
}

func renderMatrix(ctx *cmdctx.Ctx, meta *spec.ModuleMeta, nouns []string) {
	specs := ctx.Resolver.GetSpecsForModule(meta.Name)
	verbInfos := ctx.Resolver.GetVerbInfos()

	// index implemented commands: FullNoun() -> set of verbs
	implemented := map[string]map[string]bool{}
	// track which variants belong to each base noun, in declaration order
	variantsOf := map[string][]string{}
	seenVariant := map[string]bool{}
	for _, cs := range specs {
		if cs.Noun == "" {
			continue
		}
		fn := cs.FullNoun()
		if implemented[fn] == nil {
			implemented[fn] = map[string]bool{}
		}
		implemented[fn][cs.Verb] = true
		if cs.NounVariant != "" && !seenVariant[fn] {
			seenVariant[fn] = true
			variantsOf[cs.Noun] = append(variantsOf[cs.Noun], fn)
		}
	}

	// build matrix row order: each base noun followed immediately by its variants
	var matrixNouns []string
	for _, n := range nouns {
		matrixNouns = append(matrixNouns, n)
		matrixNouns = append(matrixNouns, variantsOf[n]...)
	}

	// only include verbs that appear at least once in this module, in canonical order
	activeVerbSet := map[string]bool{}
	for _, verbs := range implemented {
		for verb := range verbs {
			activeVerbSet[verb] = true
		}
	}
	var activeVerbs []string
	for _, vi := range verbInfos {
		if activeVerbSet[vi.Verb] {
			activeVerbs = append(activeVerbs, vi.Verb)
		}
	}

	check := console.GreenCheck()
	t := format.NewTable()
	t.SetOutputMirror(os.Stdout)

	colConfigs := make([]table.ColumnConfig, len(activeVerbs))
	for i := range activeVerbs {
		colConfigs[i] = table.ColumnConfig{
			Number:      i + 2,
			Align:       text.AlignCenter,
			AlignHeader: text.AlignCenter,
		}
	}
	t.SetColumnConfigs(colConfigs)

	header := make(table.Row, 1+len(activeVerbs))
	header[0] = "noun/verb"
	for i, v := range activeVerbs {
		header[i+1] = v
	}
	t.AppendHeader(header)

	for _, n := range matrixNouns {
		row := make(table.Row, 1+len(activeVerbs))
		row[0] = n
		for i := range activeVerbs {
			row[i+1] = ""
		}
		for i, v := range activeVerbs {
			if implemented[n][v] {
				row[i+1] = check
			}
		}
		t.AppendRow(row)
	}

	t.Render()
}

// RenderNounBlock builds the indented noun list for {{nouns}} substitution.
// Each line: noun, short_desc, comma-separated verbs (with :variant suffixes for variants).
func RenderNounBlock(module string, nouns []string, r cmdctx.Resolver) string {
	verbInfos := r.GetVerbInfos()

	// build per-noun verb token sets from specs
	type nounVerbs struct {
		verbs  map[string]bool // bare verb present
		tokens map[string]bool // "verb" or "verb:variant" display tokens
	}
	verbsByNoun := map[string]*nounVerbs{}
	for _, cs := range r.GetSpecsForModule(module) {
		if cs.Noun == "" {
			continue
		}
		nv := verbsByNoun[cs.Noun]
		if nv == nil {
			nv = &nounVerbs{verbs: map[string]bool{}, tokens: map[string]bool{}}
			verbsByNoun[cs.Noun] = nv
		}
		nv.verbs[cs.Verb] = true
		if cs.NounVariant != "" {
			nv.tokens[cs.Verb+" "+cs.Noun+":"+cs.NounVariant] = true
		} else {
			nv.tokens[cs.Verb] = true
		}
	}

	// build ordered verb token list for a noun: canonical verb order, variants after their base
	verbTokens := func(n string) string {
		nv := verbsByNoun[n]
		if nv == nil {
			return ""
		}
		var tokens []string
		for _, vi := range verbInfos {
			if nv.tokens[vi.Verb] {
				tokens = append(tokens, vi.Verb)
			}
			var variants []string
			for tok := range nv.tokens {
				if strings.HasPrefix(tok, vi.Verb+" ") {
					variants = append(variants, tok)
				}
			}
			sort.Strings(variants)
			tokens = append(tokens, variants...)
		}
		return strings.Join(tokens, ", ")
	}

	maxLen := 0
	for _, n := range nouns {
		if len(n) > maxLen {
			maxLen = len(n)
		}
	}

	var sb strings.Builder
	for _, n := range nouns {
		nd := r.GetNoun(n)
		padding := strings.Repeat(" ", maxLen-len(n))
		verbs := verbTokens(n)
		desc := ""
		if nd != nil {
			desc = nd.ShortDesc
		}
		if desc != "" && verbs != "" {
			fmt.Fprintf(&sb, "  %s%s    %s [%s]\n", n, padding, desc, verbs)
		} else if desc != "" {
			fmt.Fprintf(&sb, "  %s%s    %s\n", n, padding, desc)
		} else {
			fmt.Fprintf(&sb, "  %s\n", n)
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

func ListModulesFetchFn(ctx *cmdctx.Ctx, _ *spec.EndpointSpec, _, _ int, _ any) (*cmdctx.PageResult, error) {
	typeFilter := cmdctx.GetString(ctx.FlagValues, "module-type")
	var items []any
	for _, m := range ctx.Resolver.GetModuleMetas() {
		if typeFilter != "" && !strings.EqualFold(m.Type, typeFilter) {
			continue
		}
		installed := "yes"
		version := hbase.Version
		if m.ExternalBinary != "" {
			binPath, err := plugin.FindBinary(m.ExternalBinary)
			if err != nil {
				installed = "no"
				version = ""
			} else {
				version = plugin.QueryVersion(binPath)
			}
		}
		items = append(items, map[string]any{
			"module":    m.Name,
			"type":      m.Type,
			"installed": installed,
			"version":   version,
			"desc":      m.Desc,
		})
	}
	return &cmdctx.PageResult{
		Items:       items,
		StartOffset: 0,
		Last:        true,
		HasTotal:    true,
		Total:       int64(len(items)),
	}, nil
}
