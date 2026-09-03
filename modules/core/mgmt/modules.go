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
	"github.com/harness/cli/pkg/spec"
)

func GetModuleHandler(ctx *cmdctx.Ctx) error {
	return getModuleOrPluginHandler(ctx, "module", func(spec.ModuleMeta) bool { return true })
}

// GetPluginHandler shows the same domain-model output as GetModuleHandler,
// but only for modules that are plugins (see ModuleMeta.IsPlugin) — a plugin
// is a module in every respect that matters for grammar/rendering.
func GetPluginHandler(ctx *cmdctx.Ctx) error {
	return getModuleOrPluginHandler(ctx, "plugin", spec.ModuleMeta.IsPlugin)
}

func getModuleOrPluginHandler(ctx *cmdctx.Ctx, kind string, match func(spec.ModuleMeta) bool) error {
	var meta *spec.ModuleMeta
	var nameMatch *spec.ModuleMeta
	for _, m := range ctx.Resolver.GetModuleMetas() {
		if !strings.EqualFold(m.Name, ctx.Id) {
			continue
		}
		m := m
		nameMatch = &m
		if match(m) {
			meta = &m
			break
		}
	}
	if meta == nil {
		if nameMatch != nil && !nameMatch.IsPlugin() {
			// name exists but isn't a plugin — the caller almost certainly meant `get module`.
			return fmt.Errorf("%s %q not found (it's a builtin module — did you mean %q?)", kind, ctx.Id, "harness get module "+ctx.Id)
		}
		if _, ok := pluginRegistry[strings.ToLower(ctx.Id)]; ok {
			return fmt.Errorf("%s %q is not installed — to install run %q", kind, ctx.Id, "harness install plugin "+ctx.Id)
		}
		return fmt.Errorf("%s %q not found", kind, ctx.Id)
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

	// render help text from the spec (plugin specs carry it too), or a plain list
	helpText := meta.HelpText
	if helpText != "" {
		nounBlock := RenderNounBlock(meta.Name, nouns, ctx.Resolver)
		fmt.Print(colorizeHeadings(strings.ReplaceAll(helpText, "{{nouns}}", nounBlock)))
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

// colorizeHeadings bolds and blues markdown "## " / "### " heading lines, left
// untouched (and un-colored) when stdout isn't a TTY.
func colorizeHeadings(s string) string {
	if !console.IsStdoutTTY() {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "## ") || strings.HasPrefix(line, "### ") {
			lines[i] = console.WithBoldColor(console.ColorBlue, line)
		}
	}
	return strings.Join(lines, "\n")
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

	// // descStart is the column at which each noun's description begins — "  " +
	// // the noun column (padded to maxLen) + a 4-space gap. Wrapped continuation
	// // lines are indented to this column so they align under the description's
	// // first word rather than back under the noun name.
	descStart := 2 + maxLen + 4
	wrapWidth := 100 - descStart // just hardcode for now because the normal thing looks ugly in this case.
	if wrapWidth < 20 {
		wrapWidth = 20
	}
	contIndent := strings.Repeat(" ", descStart)

	var sb strings.Builder
	for _, n := range nouns {
		nd := r.GetNoun(n)
		padding := strings.Repeat(" ", maxLen-len(n))
		coloredNoun := console.WithColor(console.ColorMagenta, n)
		verbs := verbTokens(n)
		desc := ""
		if nd != nil {
			desc = nd.ShortDesc
		}

		if desc == "" {
			fmt.Fprintf(&sb, "  %s\n", coloredNoun)
			continue
		}

		content := desc
		if verbs != "" {
			content = desc + " [" + verbs + "]"
		}
		lines := strings.Split(text.WrapSoft(content, wrapWidth), "\n")

		fmt.Fprintf(&sb, "  %s%s    %s\n", coloredNoun, padding, lines[0])
		for _, line := range lines[1:] {
			fmt.Fprintf(&sb, "%s%s\n", contIndent, line)
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

// ListPluginsFetchFn lists installed plugins: modules whose spec carries a
// binary_path (dynamically installed, vs. compiled-in builtins). Trusts the
// spec's provenance for version/source/installed_at — never execs the binary.
func ListPluginsFetchFn(ctx *cmdctx.Ctx, _ *spec.EndpointSpec, _, _ int, _ any) (*cmdctx.PageResult, error) {
	var items []any
	for _, m := range ctx.Resolver.GetModuleMetas() {
		if !m.IsPlugin() {
			continue
		}
		items = append(items, map[string]any{
			"plugin":       m.Name,
			"version":      m.Version,
			"binary_path":  m.BinaryPath,
			"source":       m.Source,
			"installed_at": m.InstalledAt,
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

func ListModulesFetchFn(ctx *cmdctx.Ctx, _ *spec.EndpointSpec, _, _ int, _ any) (*cmdctx.PageResult, error) {
	typeFilter := cmdctx.GetString(ctx.FlagValues, "module-type")
	var items []any
	seen := map[string]bool{}
	for _, m := range ctx.Resolver.GetModuleMetas() {
		seen[m.Name] = true
		if typeFilter != "" && !strings.EqualFold(m.Type, typeFilter) {
			continue
		}
		// Builtins ship with the CLI, so they have no independent version or
		// install state.
		installed := "-"
		version := "-"
		if m.BinaryPath != "" {
			// Plugin: trust the spec's provenance, which install captured from
			// --identity. Listing never execs a binary to read a version.
			installed = "yes"
			version = m.Version
			if _, err := os.Stat(hbase.ExpandHomeDir(m.BinaryPath)); err != nil {
				installed = "no"
				version = "-"
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
	if typeFilter == "" || strings.EqualFold(typeFilter, "plugin") {
		for _, m := range UninstalledRegistryPlugins(seen) {
			items = append(items, map[string]any{
				"module":    m.Name,
				"type":      m.Type,
				"installed": "no",
				"version":   "-",
				"desc":      m.Desc,
			})
		}
	}
	return &cmdctx.PageResult{
		Items:       items,
		StartOffset: 0,
		Last:        true,
		HasTotal:    true,
		Total:       int64(len(items)),
	}, nil
}
