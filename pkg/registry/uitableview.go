// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"golang.org/x/term"

	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/console"
	"github.com/harness/cli/pkg/endpoint"
	"github.com/harness/cli/pkg/exprenv"
	"github.com/harness/cli/pkg/extractutil"
	"github.com/harness/cli/pkg/format"
	"github.com/harness/cli/pkg/spec"
	"github.com/harness/cli/pkg/strutil"
	"github.com/harness/cli/pkg/tui"
)

// uiDetailModel holds the state for the drilldown detail overlay.
type uiDetailModel struct {
	id      string
	loading bool
	err     string
	lines   []string
	scroll  int
	// upIds maps an "up" ui_commands entry's key to its up_id_expr result,
	// evaluated against this detail's item once when it's fetched (see
	// fetchDetail) since that's the only place the raw item data is available.
	upIds map[string]string
}

// uiLinkTarget carries a resolved link-type ui_commands entry to follow once
// the bubbletea program exits.
type uiLinkTarget struct {
	verb string
	noun string
	id   string
}

// uiDetailMsg is sent when a background detail fetch completes.
type uiDetailMsg struct {
	content string
	upIds   map[string]string
	err     error
}

func termSize() (width, height int, err error) {
	w, h, err := term.GetSize(int(syscall.Stdout))
	if err != nil || w <= 0 || h <= 0 {
		w2, h2, err2 := term.GetSize(int(os.Stdout.Fd()))
		if err2 != nil {
			return 0, 0, err2
		}
		return w2, h2, nil
	}
	return w, h, nil
}

// uiOverheadLines: title + blank + blank + status bar.
const uiOverheadLines = 4

// uiMinColWidth is the floor for any column during shrinking.
const uiMinColWidth = 4

// uiColPad is extra padding added to each natural column width.
const uiColPad = 2

// uiPageMsg is sent when a background page fetch completes.
type uiPageMsg struct {
	page  int
	rows  []tui.Row
	items []any
	total int64
	last  bool
	err   error
}

// uiTableModel is the bubbletea model for the --ui paged table view.
type uiTableModel struct {
	ctx     *cmdctx.Ctx
	ep      *spec.EndpointSpec
	tspec   *spec.TableSpec
	fields  []spec.FieldDef
	exprEnv map[string]any

	t        tui.TableModel
	colDefs  []tui.Column
	rawRows  []tui.Row // last page's raw rows, for re-fitting on resize
	rawItems []any     // parallel to rawRows — raw API items for get_id_expr evaluation

	// drilldown
	getCs             *spec.CommandSpec
	uiCommands        []spec.UICommand // getCs.Noun's ui_commands, if any; drives detail hotkeys
	detailOnly        bool             // true when there is no browse table underneath (Case 4)
	detailMode        bool
	detail            uiDetailModel
	activeTextKey     string // key of the ui_commands text entry currently rendered in detail
	printOnExit       []string
	launchUIId        string
	launchUIHandlerFn string        // view entry's ui_handler_fn, set on quit
	linkTarget        *uiLinkTarget // link entry to follow, set on quit

	// picker mode — set via newUIPickerModel; enter selects and quits
	pickerMode       bool
	selectedId       string
	cmdPreviewVerb   string // e.g. "get artifact_version"
	cmdPreviewDone   string // already-picked prefix, e.g. "mikereg/"
	cmdPreviewSuffix string // remaining-steps placeholder, e.g. "/…" or ""

	titleLine string

	page     int
	pageSize int
	total    int64
	hasTotal bool
	isLast   bool
	loading  bool
	err      string

	// search
	searchTerm string
	searchMode bool
	searchBuf  string
	hasSearch  bool

	// column picker
	colPickMode   bool
	colPickSel    []bool
	colPickCursor int
	colPickScroll int

	width  int
	height int
}

// tableHeight returns the number of data rows visible (excludes header row).
func tableHeight(termHeight int) int {
	return max(termHeight-uiOverheadLines-1, 2)
}

func newUITableModel(
	ctx *cmdctx.Ctx,
	ep *spec.EndpointSpec,
	tspec *spec.TableSpec,
	fields []spec.FieldDef,
	exprEnv map[string]any,
	titleLine string,
	termWidth, termHeight int,
	getCs *spec.CommandSpec,
) uiTableModel {
	pageSize := tableHeight(termHeight)
	colDefs := placeholderColumns(tspec, termWidth)
	t := tui.NewTable(colDefs, pageSize, termWidth)

	_, hasSearch := ctx.FlagValues["search"]

	var uiCommands []spec.UICommand
	if getCs != nil && ctx.Resolver != nil {
		if nd := ctx.Resolver.GetNoun(getCs.Noun); nd != nil {
			uiCommands = nd.UICommands
		}
	}

	return uiTableModel{
		ctx:        ctx,
		ep:         ep,
		tspec:      tspec,
		fields:     fields,
		exprEnv:    exprEnv,
		t:          t,
		colDefs:    colDefs,
		titleLine:  titleLine,
		pageSize:   pageSize,
		loading:    true,
		width:      termWidth,
		height:     termHeight,
		hasSearch:  hasSearch,
		getCs:      getCs,
		uiCommands: uiCommands,
	}
}

// newUIDetailModel builds a detail-only overlay model (Case 4): no browse table
// underneath, rendered directly at the noun's default text entry.
func newUIDetailModel(ctx *cmdctx.Ctx, getCs *spec.CommandSpec, id string, uiCommands []spec.UICommand, defaultKey string, termWidth, termHeight int) uiTableModel {
	return uiTableModel{
		ctx:           ctx,
		getCs:         getCs,
		uiCommands:    uiCommands,
		activeTextKey: defaultKey,
		detailOnly:    true,
		detailMode:    true,
		detail:        uiDetailModel{id: id, loading: true},
		width:         termWidth,
		height:        termHeight,
	}
}

// defaultTextKey returns the key of the ui_commands entry marked default: true,
// or "" if none (which happens when ucs is empty — the legacy, non-data-driven path).
func defaultTextKey(ucs []spec.UICommand) string {
	for _, uc := range ucs {
		if uc.UICommandType == spec.UICommandText && uc.Default {
			return uc.Key
		}
	}
	return ""
}

// pickerCursorId returns the completion id of the currently highlighted row,
// or "…" when loading or no items are available.
func (m uiTableModel) pickerCursorId() string {
	if m.loading || len(m.rawItems) == 0 {
		return "…"
	}
	cursor := m.t.Cursor()
	if cursor >= len(m.rawItems) {
		return "…"
	}
	if m.ep.Completion == nil || m.ep.Completion.IdExpr == "" {
		return "…"
	}
	env := exprenv.WithIt(m.exprEnv, m.rawItems[cursor])
	if id := exprenv.EvalExpr(env, m.ep.Completion.IdExpr); id != "" {
		return id
	}
	return "…"
}

// newUIPickerModel builds a table model in picker mode: enter selects and quits.
// idExpr is evaluated against the raw item to produce the selection string.
func newUIPickerModel(
	ctx *cmdctx.Ctx,
	ep *spec.EndpointSpec,
	tspec *spec.TableSpec,
	fields []spec.FieldDef,
	exprEnv map[string]any,
	titleLine string,
	termWidth, termHeight int,
) uiTableModel {
	m := newUITableModel(ctx, ep, tspec, fields, exprEnv, titleLine, termWidth, termHeight, nil)
	m.pickerMode = true
	return m
}

// placeholderColumns returns equal-width columns used before data arrives.
func placeholderColumns(tspec *spec.TableSpec, termWidth int) []tui.Column {
	if tspec == nil || len(tspec.Columns) == 0 {
		return []tui.Column{{Header: "ID", Width: termWidth - tui.GutterWidth - 2}}
	}
	n := len(tspec.Columns)
	w := max((termWidth-tui.GutterWidth-2)/n, uiMinColWidth)
	cols := make([]tui.Column, n)
	for i, c := range tspec.Columns {
		cols[i] = tui.Column{Header: c.Header, Width: w}
	}
	return cols
}

// naturalColWidths returns the natural content width for each column:
// max(header_len, max_visible_cell_len) + uiColPad.
func naturalColWidths(tspec *spec.TableSpec, rows []tui.Row) []int {
	n := len(tspec.Columns)
	widths := make([]int, n)
	for i, c := range tspec.Columns {
		widths[i] = len([]rune(c.Header)) + uiColPad
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < n {
				// measure visible width, not raw width (cells may contain ANSI)
				w := len([]rune(console.StripANSI(cell))) + uiColPad
				if w > widths[i] {
					widths[i] = w
				}
			}
		}
	}
	return widths
}

// columnReduceOneWidth finds the widest column above minWidth and reduces it by 1.
func columnReduceOneWidth(cols []tui.Column, minWidth int) {
	maxW, maxI := 0, -1
	for i, c := range cols {
		if c.Width > maxW {
			maxW = c.Width
			maxI = i
		}
	}
	if maxI >= 0 && cols[maxI].Width > minWidth {
		cols[maxI].Width--
	}
}

// fitColumns builds []tui.Column with data-driven widths, then shrinks the
// widest column one step at a time until everything fits in termWidth.
func fitColumns(tspec *spec.TableSpec, rows []tui.Row, termWidth int) []tui.Column {
	if tspec == nil || len(tspec.Columns) == 0 {
		return []tui.Column{{Header: "ID", Width: termWidth - tui.GutterWidth - 2}}
	}

	widths := naturalColWidths(tspec, rows)

	cols := make([]tui.Column, len(tspec.Columns))
	for i, c := range tspec.Columns {
		cols[i] = tui.Column{Header: c.Header, Width: widths[i]}
	}

	// overhead = gutter + (n-1) inner separators
	overhead := tui.GutterWidth + (len(cols) - 1)
	for {
		total := overhead
		for _, c := range cols {
			total += c.Width
		}
		if total <= termWidth {
			break
		}
		canReduce := false
		for _, c := range cols {
			if c.Width > uiMinColWidth {
				canReduce = true
				break
			}
		}
		if !canReduce {
			break
		}
		columnReduceOneWidth(cols, uiMinColWidth)
	}

	return cols
}

func (m uiTableModel) Init() tea.Cmd {
	if m.detailOnly {
		return m.fetchDetail(m.detail.id)
	}
	return m.fetchPage(0)
}

func (m uiTableModel) fetchPage(page int) tea.Cmd {
	ctx := m.ctx
	ep := m.ep
	tspec := m.tspec
	exprEnv := m.exprEnv
	pageSize := m.pageSize
	offset := page * pageSize
	hasSearch := m.hasSearch
	searchTerm := m.searchTerm

	return func() tea.Msg {
		if hasSearch {
			ctxCopy := *ctx
			fv := make(map[string]any, len(ctx.FlagValues))
			for k, v := range ctx.FlagValues {
				fv[k] = v
			}
			fv["search"] = searchTerm
			ctxCopy.FlagValues = fv
			ctx = &ctxCopy
		}

		items, meta, err := endpoint.FetchRange(ctx, ep, offset, pageSize)
		if err != nil {
			return uiPageMsg{page: page, err: err}
		}

		// rowItems are unwrapped for column expression evaluation; items stays
		// as the raw API objects so get_id_expr and completion id_expr work correctly.
		rowItems := items
		if ep.ItemItemExpr != "" {
			unwrapped := make([]any, 0, len(items))
			for _, item := range items {
				if v, ok := exprenv.EvalExprAny(exprenv.WithIt(exprEnv, item), ep.ItemItemExpr); ok {
					unwrapped = append(unwrapped, v)
				} else {
					unwrapped = append(unwrapped, item)
				}
			}
			rowItems = unwrapped
		}

		rows, err := itemsToTableRows(rowItems, tspec, exprEnv)
		if err != nil {
			return uiPageMsg{page: page, err: err}
		}

		var total int64
		last := meta == nil || (meta.Count < pageSize)
		if meta != nil && meta.HasTotal {
			total = meta.Total
			last = (offset + meta.Count) >= int(meta.Total)
		}

		return uiPageMsg{page: page, rows: rows, items: items, total: total, last: last}
	}
}

func formatUICell(val any) string {
	if val == nil {
		return ""
	}
	return strutil.Stringify(val)
}

func itemsToTableRows(items []any, tspec *spec.TableSpec, exprEnv map[string]any) ([]tui.Row, error) {
	if tspec == nil || len(tspec.Columns) == 0 {
		return nil, fmt.Errorf("no table spec available for UI view")
	}

	rows := make([]tui.Row, 0, len(items))
	for _, item := range items {
		env := exprenv.WithIt(exprEnv, item)
		row := make(tui.Row, len(tspec.Columns))
		for j, col := range tspec.Columns {
			val, _ := exprenv.EvalExprAny(env, col.Expr)
			row[j] = formatUICell(val)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// applyPage fits columns to the current terminal width and updates the table widget.
func (m *uiTableModel) applyPage(rawRows []tui.Row, rawItems []any) {
	m.rawRows = rawRows
	m.rawItems = rawItems
	cols := fitColumns(m.tspec, rawRows, m.width)
	m.colDefs = cols
	m.t.SetColumns(cols)
	m.t.SetRows(rawRows)
	m.t.GotoTop()
}

// enterColPick initialises the column picker from the current tspec.
func (m *uiTableModel) enterColPick() {
	active := make(map[string]bool)
	if m.tspec != nil {
		for _, c := range m.tspec.Columns {
			active[c.Header] = true
		}
	}
	m.colPickSel = make([]bool, len(m.fields))
	for i, f := range m.fields {
		m.colPickSel[i] = active[fieldLabel(f)]
	}
	m.colPickCursor = 0
	m.colPickScroll = 0
	m.colPickMode = true
}

// applyColPick builds a new tspec from checked fields and refreshes the page.
func (m *uiTableModel) applyColPick() tea.Cmd {
	m.colPickMode = false
	var selected []spec.FieldDef
	for i, on := range m.colPickSel {
		if on {
			selected = append(selected, m.fields[i])
		}
	}
	if len(selected) == 0 {
		return nil
	}
	m.tspec = &spec.TableSpec{Columns: FieldsToTableColumns(selected)}
	m.loading = true
	m.err = ""
	return m.fetchPage(m.page)
}

// activeDetailCs resolves the get command backing the currently-shown detail
// pane: the noun's active ui_commands text entry when one is set, else getCs
// unchanged (the legacy, non-data-driven path for nouns with no ui_commands).
func (m uiTableModel) activeDetailCs() *spec.CommandSpec {
	for _, uc := range m.uiCommands {
		if uc.UICommandType == spec.UICommandText && uc.Key == m.activeTextKey {
			if m.ctx.Resolver != nil {
				if cs := m.ctx.Resolver.GetSpec(VerbGet, uc.Noun); cs != nil {
					return cs
				}
			}
		}
	}
	return m.getCs
}

// dispatchUICommandKey handles a hotkey press against the active noun's
// ui_commands list: re-renders in place for a text entry, or queues a view/link
// hand-off and quits for view/link entries. handled=false means key isn't bound.
func (m uiTableModel) dispatchUICommandKey(key string) (uiTableModel, tea.Cmd, bool) {
	for _, uc := range m.uiCommands {
		if uc.Key != key {
			continue
		}
		switch uc.UICommandType {
		case spec.UICommandText:
			if uc.Key == m.activeTextKey {
				return m, nil, true
			}
			m.activeTextKey = uc.Key
			m.detail.loading = true
			m.detail.err = ""
			return m, m.fetchDetail(m.detail.id), true
		case spec.UICommandView:
			m.launchUIId = m.detail.id
			m.launchUIHandlerFn = uc.UIHandlerFn
			return m, tea.Quit, true
		case spec.UICommandLink:
			m.linkTarget = &uiLinkTarget{verb: uc.Verb, noun: uc.Noun, id: m.detail.id}
			return m, tea.Quit, true
		case spec.UICommandUp:
			id := m.detail.upIds[uc.Key]
			if id == "" && uc.UpIdExpr != "" {
				// up_id_expr was declared but the current item had nothing at
				// that path (e.g. an externally-reported check) — nothing to
				// jump to.
				return m, nil, true
			}
			verb := uc.Verb
			if verb == "" {
				verb = VerbGet
			}
			m.linkTarget = &uiLinkTarget{verb: verb, noun: uc.Noun, id: id}
			return m, tea.Quit, true
		}
	}
	return m, nil, false
}

// fetchDetail fetches the get endpoint for id and renders it to a string.
func (m uiTableModel) fetchDetail(id string) tea.Cmd {
	ctx := m.ctx
	cs := m.activeDetailCs()
	ep := cs.Endpoint
	return func() tea.Msg {
		detailCtx := buildDetailCtx(ctx, cs, id)
		var buf strings.Builder
		detailCtx.FormatFlags = cmdctx.FormatFlags{Format: "text"}
		result, err := CallEndpoint(detailCtx, ep)
		if err != nil {
			return uiDetailMsg{err: err}
		}
		exprEnv := exprenv.Make(detailCtx)
		fields := resolveFieldsForCommand(detailCtx, ep)
		var textFmt cmdctx.TextFormatterFn
		if ep.TextFormatter != "" && ctx.Resolver != nil {
			textFmt = ctx.Resolver.ResolveTextFormatter(ep.TextFormatter)
		}
		if textFmt == nil && len(fields) > 0 {
			textFmt = buildDeclTextFmt(fields, ep, exprEnv)
		}
		if textFmt == nil {
			return uiDetailMsg{err: fmt.Errorf("no text formatter available for %s %s", cs.Verb, cs.Noun)}
		}
		payload := any(result)
		if ep.ItemExpr != "" {
			if v, ok := exprenv.EvalExprAny(exprenv.WithIt(exprEnv, result), ep.ItemExpr); ok {
				payload = v
			}
		}
		if err := textFmt(&buf, extractutil.MakeDataAccessor(exprEnv, payload)); err != nil {
			return uiDetailMsg{err: err}
		}
		var upIds map[string]string
		itEnv := exprenv.WithIt(exprEnv, payload)
		for _, uc := range m.uiCommands {
			if uc.UICommandType == spec.UICommandUp {
				if upIds == nil {
					upIds = make(map[string]string)
				}
				upIds[uc.Key] = exprenv.EvalExpr(itEnv, uc.UpIdExpr)
			}
		}
		return uiDetailMsg{content: buf.String(), upIds: upIds}
	}
}

// detailView renders the drilldown detail overlay.
func (m uiTableModel) detailView() string {
	var b strings.Builder
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(tui.CLIAccent))
	subtleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.CLITextMuted))

	noun := strings.ReplaceAll(m.activeDetailCs().Noun, "_", " ")
	title := fmt.Sprintf("get %s  %s", noun, m.detail.id)
	b.WriteString(titleStyle.Render(tui.TruncateANSI(title, m.width-1)) + "\n")
	b.WriteString("\n")

	visRows := m.height - uiOverheadLines - 1
	if visRows < 1 {
		visRows = 1
	}

	if m.detail.loading {
		b.WriteString(subtleStyle.Render("  Loading…") + "\n")
	} else if m.detail.err != "" {
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(tui.CLIError)).Render("  error: "+m.detail.err) + "\n")
	} else {
		end := m.detail.scroll + visRows
		if end > len(m.detail.lines) {
			end = len(m.detail.lines)
		}
		for _, line := range m.detail.lines[m.detail.scroll:end] {
			b.WriteString(line + "\n")
		}
	}

	b.WriteString("\n")
	backOrQuit := "esc back  q quit"
	if m.detailOnly {
		backOrQuit = "esc/q quit"
	}
	hint := "  ↑↓/jk scroll  " + backOrQuit
	if !m.detail.loading && m.detail.err == "" {
		hint += "  p print+exit"
		for _, uc := range m.uiCommands {
			hint += fmt.Sprintf("  %s %s", uc.Key, uiCommandHintLabel(uc))
		}
	}
	b.WriteString(subtleStyle.Render(hint) + "\n")
	return b.String()
}

// uiCommandHintLabel renders the short hint text shown next to a ui_commands
// entry's hotkey in the detail overlay's status line.
func uiCommandHintLabel(uc spec.UICommand) string {
	if uc.Label != "" {
		return uc.Label
	}
	switch uc.UICommandType {
	case spec.UICommandText:
		return strings.ReplaceAll(uc.Noun, "_", " ")
	case spec.UICommandLink:
		return uc.Verb + " " + strings.ReplaceAll(uc.Noun, "_", " ")
	case spec.UICommandView:
		return "view"
	case spec.UICommandUp:
		return "up " + strings.ReplaceAll(uc.Noun, "_", " ")
	default:
		return ""
	}
}

// colPickView renders the interactive column picker overlay.
func (m uiTableModel) colPickView() string {
	var b strings.Builder
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(tui.CLIAccent))
	subtleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.CLITextMuted))
	cursorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.CLIAccent)).Bold(true)
	checkedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.CLIBrightWhite)).Bold(true)

	b.WriteString(titleStyle.Render("  columns") + "\n\n")

	visRows := max(m.height-uiOverheadLines-1, 3)
	end := m.colPickScroll + visRows
	if end > len(m.fields) {
		end = len(m.fields)
	}

	for i := m.colPickScroll; i < end; i++ {
		check := "[ ]"
		if m.colPickSel[i] {
			check = "[✓]"
		}
		plain := fmt.Sprintf("  %s %s", check, fieldLabel(m.fields[i]))
		var line string
		if i == m.colPickCursor {
			line = cursorStyle.Render(plain)
		} else if m.colPickSel[i] {
			line = checkedStyle.Render(plain)
		} else {
			line = subtleStyle.Render(plain)
		}
		b.WriteString(line + "\n")
	}

	b.WriteString("\n")
	b.WriteString(subtleStyle.Render("  ↑↓ move  space toggle  enter apply  esc cancel") + "\n")
	return b.String()
}

func (m uiTableModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		pageSize := tableHeight(msg.Height)
		m.pageSize = pageSize
		m.t.SetHeight(pageSize)
		m.t.SetWidth(msg.Width)
		if len(m.rawRows) > 0 {
			m.applyPage(m.rawRows, m.rawItems)
		} else {
			m.colDefs = placeholderColumns(m.tspec, msg.Width)
			m.t.SetColumns(m.colDefs)
		}
		return m, nil

	case uiDetailMsg:
		m.detail.loading = false
		if msg.err != nil {
			m.detail.err = msg.err.Error()
		} else {
			m.detail.lines = strings.Split(strings.TrimRight(msg.content, "\n"), "\n")
			m.detail.scroll = 0
			m.detail.upIds = msg.upIds
		}
		return m, nil

	case uiPageMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.err = ""
		m.applyPage(msg.rows, msg.items)
		if msg.total > 0 {
			m.hasTotal = true
			m.total = msg.total
		}
		m.isLast = msg.last
		return m, nil

	case tea.KeyPressMsg:
		if m.colPickMode {
			switch msg.String() {
			case "q", "ctrl+c":
				return m, tea.Quit
			case "up", "k":
				if m.colPickCursor > 0 {
					m.colPickCursor--
					if m.colPickCursor < m.colPickScroll {
						m.colPickScroll = m.colPickCursor
					}
				}
			case "down", "j":
				if m.colPickCursor < len(m.fields)-1 {
					m.colPickCursor++
					visRows := max(m.height-uiOverheadLines-1, 3)
					if m.colPickCursor >= m.colPickScroll+visRows {
						m.colPickScroll = m.colPickCursor - visRows + 1
					}
				}
			case " ", "space":
				m.colPickSel[m.colPickCursor] = !m.colPickSel[m.colPickCursor]
			case "enter":
				return m, m.applyColPick()
			case "esc":
				m.colPickMode = false
			}
			return m, nil
		}

		if m.detailMode {
			visRows := m.height - uiOverheadLines - 1
			switch msg.String() {
			case "q", "ctrl+c":
				return m, tea.Quit
			case "p":
				if !m.detail.loading && m.detail.err == "" {
					m.printOnExit = m.detail.lines
					return m, tea.Quit
				}
			case "esc", "backspace":
				if m.detailOnly {
					return m, tea.Quit
				}
				m.detailMode = false
			case "up", "k":
				if m.detail.scroll > 0 {
					m.detail.scroll--
				}
			case "down", "j":
				if m.detail.scroll < len(m.detail.lines)-visRows {
					m.detail.scroll++
				}
			case "pgup":
				m.detail.scroll -= visRows
				if m.detail.scroll < 0 {
					m.detail.scroll = 0
				}
			case "pgdown":
				m.detail.scroll += visRows
				if maxScroll := len(m.detail.lines) - visRows; m.detail.scroll > maxScroll {
					m.detail.scroll = maxScroll
				}
				if m.detail.scroll < 0 {
					m.detail.scroll = 0
				}
			default:
				if !m.detail.loading && m.detail.err == "" {
					if newM, cmd, handled := m.dispatchUICommandKey(msg.String()); handled {
						return newM, cmd
					}
				}
			}
			return m, nil
		}

		if m.searchMode {
			switch msg.String() {
			case "ctrl+c":
				return m, tea.Quit
			case "enter":
				m.searchTerm = m.searchBuf
				m.searchMode = false
				m.page = 0
				m.loading = true
				m.err = ""
				return m, m.fetchPage(0)
			case "esc":
				hadSearch := m.searchTerm != ""
				m.searchMode = false
				m.searchBuf = ""
				m.searchTerm = ""
				if hadSearch {
					m.page = 0
					m.loading = true
					m.err = ""
					return m, m.fetchPage(0)
				}
			case "backspace", "ctrl+h":
				if len(m.searchBuf) > 0 {
					runes := []rune(m.searchBuf)
					m.searchBuf = string(runes[:len(runes)-1])
				}
			default:
				if len(msg.String()) == 1 {
					r := rune(msg.String()[0])
					if unicode.IsPrint(r) {
						m.searchBuf += string(r)
					}
				}
			}
			return m, nil
		}

		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit

		case "enter":
			if m.pickerMode && !m.loading && len(m.rawItems) > 0 {
				cursor := m.t.Cursor()
				if cursor < len(m.rawItems) {
					item := m.rawItems[cursor]
					env := exprenv.WithIt(m.exprEnv, item)
					if m.ep.Completion != nil && m.ep.Completion.IdExpr != "" {
						m.selectedId = exprenv.EvalExpr(env, m.ep.Completion.IdExpr)
					}
					return m, tea.Quit
				}
			}
			if m.getCs != nil && !m.loading && len(m.rawItems) > 0 {
				cursor := m.t.Cursor()
				if cursor < len(m.rawItems) {
					item := m.rawItems[cursor]
					env := exprenv.WithIt(m.exprEnv, item)
					id := exprenv.EvalExpr(env, m.ep.GetIdExpr)
					if id != "" {
						m.detailMode = true
						m.activeTextKey = defaultTextKey(m.uiCommands)
						m.detail = uiDetailModel{id: id, loading: true}
						return m, m.fetchDetail(id)
					}
				}
			}

		case "c":
			if len(m.fields) > 0 {
				m.enterColPick()
				return m, nil
			}

		case "/":
			if m.hasSearch {
				m.searchMode = true
				m.searchBuf = m.searchTerm
				return m, nil
			}

		case "right", "l":
			if !m.loading && !m.isLast {
				m.page++
				m.loading = true
				m.err = ""
				return m, m.fetchPage(m.page)
			}
			return m, nil

		case "left", "h":
			if !m.loading && m.page > 0 {
				m.page--
				m.loading = true
				m.err = ""
				return m, m.fetchPage(m.page)
			}
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.t, cmd = m.t.Update(msg)
	return m, cmd
}

const uiMinHeight = 12
const uiMinWidth = 50

func (m uiTableModel) View() tea.View {
	var b strings.Builder

	if m.height < uiMinHeight || m.width < uiMinWidth {
		errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.CLIError))
		subtleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.CLITextMuted))
		b.WriteString(errStyle.Render("  terminal too small") + "\n")
		b.WriteString(subtleStyle.Render(fmt.Sprintf("  min %dx%d  current %dx%d", uiMinWidth, uiMinHeight, m.width, m.height)) + "\n")
		b.WriteString(subtleStyle.Render("  q quit") + "\n")
		v := tea.NewView(b.String())
		v.AltScreen = true
		return v
	}

	if m.detailMode {
		b.WriteString(m.detailView())
		v := tea.NewView(b.String())
		v.AltScreen = true
		return v
	}

	if m.colPickMode {
		b.WriteString(m.colPickView())
		v := tea.NewView(b.String())
		v.AltScreen = true
		return v
	}

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(tui.CLIAccent))
	subtleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.CLITextMuted))

	titleLine := m.titleLine
	if m.pickerMode && m.cmdPreviewVerb != "" {
		cursorId := m.pickerCursorId()
		titleLine = m.cmdPreviewVerb + " " + m.cmdPreviewDone + cursorId + m.cmdPreviewSuffix
	}
	b.WriteString(titleStyle.Render(tui.TruncateANSI(titleLine, m.width-1)) + "\n")
	b.WriteString("\n")

	if m.loading {
		b.WriteString(m.t.HeaderView())
		b.WriteString("\n")
		b.WriteString(subtleStyle.Render("  Loading…") + "\n")
		for i := 2; i < m.pageSize; i++ {
			b.WriteString("\n")
		}
	} else if len(m.t.Rows()) == 0 {
		b.WriteString(m.t.HeaderView())
		b.WriteString("\n")
		noResultsMsg := "  no results"
		if m.searchTerm != "" {
			noResultsMsg = fmt.Sprintf("  no results (searching for %q)", m.searchTerm)
		}
		b.WriteString(subtleStyle.Render(tui.TruncateANSI(noResultsMsg, m.width-1)) + "\n")
		for i := 2; i < m.pageSize; i++ {
			b.WriteString("\n")
		}
	} else {
		b.WriteString(m.t.View())
		// pad short pages so the status bar stays pinned at the same row
		for i := len(m.t.Rows()); i < m.pageSize; i++ {
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(m.statusBar())

	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
}

func (m uiTableModel) statusBar() string {
	subtle := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.CLITextMuted))
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.CLIAccent)).Bold(true)

	clip := func(s string) string { return tui.TruncateANSI(s, m.width-1) }

	if m.err != "" {
		return clip(lipgloss.NewStyle().Foreground(lipgloss.Color(tui.CLIError)).Render("  error: "+m.err)) + "\n"
	}

	if m.searchMode {
		cursor := accent.Render("█")
		return clip("  "+accent.Render("/")+m.searchBuf+cursor+subtle.Render("  enter confirm  esc cancel")) + "\n"
	}

	offset := m.page * m.pageSize
	count := len(m.t.Rows())

	var rangeStr string
	if count > 0 {
		first := offset + 1
		last := offset + count
		if m.hasTotal {
			rangeStr = fmt.Sprintf("%d-%d of %d", first, last, m.total)
		} else {
			rangeStr = fmt.Sprintf("%d-%d", first, last)
		}
	} else {
		rangeStr = "no results"
	}

	var nav []string
	if m.page > 0 {
		nav = append(nav, accent.Render("◀")+" prev")
	}
	if !m.isLast {
		nav = append(nav, "next "+accent.Render("▶"))
	}
	navStr := strings.Join(nav, "  ")

	parts := []string{"  " + rangeStr}
	if navStr != "" {
		parts = append(parts, "  "+navStr)
	}
	if m.hasSearch {
		if m.searchTerm != "" {
			parts = append(parts, "  "+accent.Render("/")+" "+m.searchTerm+"  "+accent.Render("(searching)"))
		} else {
			parts = append(parts, subtle.Render("  / search"))
		}
	}
	if m.pickerMode && len(m.t.Rows()) > 0 {
		parts = append(parts, accent.Render("  enter")+" select")
	} else if m.getCs != nil && len(m.t.Rows()) > 0 {
		parts = append(parts, subtle.Render("  enter detail"))
	}
	if len(m.fields) > 0 {
		parts = append(parts, subtle.Render("  c columns"))
	}
	parts = append(parts, subtle.Render("  q quit"))

	return clip(strings.Join(parts, "")) + "\n"
}

// PickerPreview carries the command-preview fields shown in the picker title.
type PickerPreview struct {
	Verb   string // e.g. "get artifact_version"
	Done   string // already-resolved prefix, e.g. "mikereg/" (empty on first step)
	Suffix string // remaining-steps placeholder, e.g. "/…" or "" on last step
}

// RunUIPicker runs a picker TUI over a list endpoint and returns the selected id.
// Returns an error if the user cancels without selecting.
// ctx should already have Verb/Noun/Auth/Level set for the list endpoint.
// titleLine is the fallback title; preview, when non-nil, enables the live command preview.
func RunUIPicker(ctx *cmdctx.Ctx, ep *spec.EndpointSpec, titleLine string, preview *PickerPreview) (string, error) {
	if !console.IsBothTTY() {
		return "", fmt.Errorf("--ui requires an interactive terminal (TTY)")
	}
	fields := resolveFieldsForCommand(ctx, ep)
	tspec := buildTspec(ep.Columns, fields)
	exprEnv := exprenv.Make(ctx)

	termWidth, termHeight := 120, 30
	if w, h, err := termSize(); err == nil {
		termWidth, termHeight = w, h
	}

	m := newUIPickerModel(ctx, ep, tspec, fields, exprEnv, titleLine, termWidth, termHeight)
	if preview != nil {
		m.cmdPreviewVerb = preview.Verb
		m.cmdPreviewDone = preview.Done
		m.cmdPreviewSuffix = preview.Suffix
	}
	p := tea.NewProgram(m)
	finalModel, err := p.Run()
	if err != nil {
		return "", err
	}
	fm, ok := finalModel.(uiTableModel)
	if !ok || fm.selectedId == "" {
		return "", fmt.Errorf("no id selected")
	}
	return fm.selectedId, nil
}

// RunUITable runs the bubbletea paged table TUI for a list endpoint.
func RunUITable(ctx *cmdctx.Ctx, ep *spec.EndpointSpec) error {
	// Look up the get spec for drilldown; nil disables the feature.
	var getCs *spec.CommandSpec
	if ep.GetIdExpr != "" && ep.GetIdExpr != "-" && ctx.Resolver != nil {
		getCs = ctx.Resolver.GetSpec(VerbGet, ctx.Noun)
	}
	return RunUITableForGet(ctx, ep, getCs)
}

// RunUITableForGet runs the bubbletea paged table TUI for a list endpoint, with the
// enter-to-drilldown detail overlay driven by getCs (which may be nil to disable it):
// its text_formatter renders the row detail, and its noun's ui_commands (if any)
// drive the detail pane's hotkeys. Used both by "list <noun> --ui" and, for a get
// command's --ui with no id given, as the final step once any completion_seq prefix
// has been resolved — so picking an id and viewing/printing it go through the exact
// same screen either way.
func RunUITableForGet(ctx *cmdctx.Ctx, ep *spec.EndpointSpec, getCs *spec.CommandSpec) error {
	if !console.IsBothTTY() {
		return fmt.Errorf("--ui requires an interactive terminal (TTY)")
	}
	fields := resolveFieldsForCommand(ctx, ep)
	tspec := buildTspec(ep.Columns, fields)

	exprEnv := exprenv.Make(ctx)

	if cols := cmdctx.GetString(ctx.FlagValues, "columns"); cols != "" {
		var base []spec.TableColumn
		if tspec != nil {
			base = tspec.Columns
		}
		overrideCols, err := format.ApplyColumns(fields, base, cols)
		if err != nil {
			return err
		}
		tspec = &spec.TableSpec{Columns: overrideCols}
	}

	noun := strings.ReplaceAll(ctx.Noun, "_", " ")
	titleLine := ctx.Verb + " " + noun

	var parts []string
	if ctx.Level != "" {
		parts = append(parts, "scope: "+ctx.Level)
	}
	if len(parts) > 0 {
		titleLine += "   " + strings.Join(parts, "   ")
	}

	initialSearch := cmdctx.GetString(ctx.FlagValues, "search")

	termWidth, termHeight := 120, 30
	if w, h, err := termSize(); err == nil {
		termWidth, termHeight = w, h
	}

	m := newUITableModel(ctx, ep, tspec, fields, exprEnv, titleLine, termWidth, termHeight, getCs)
	m.searchTerm = initialSearch
	p := tea.NewProgram(m)
	finalModel, err := p.Run()
	if err != nil {
		return err
	}
	fm, ok := finalModel.(uiTableModel)
	if !ok {
		return nil
	}
	return finishUIExit(ctx, fm)
}

// finishUIExit handles common post-Run() actions for the detail overlay: printing
// content queued via "p", following a link entry to a different noun's screen, or
// handing off to a view entry's ui_handler_fn.
func finishUIExit(ctx *cmdctx.Ctx, fm uiTableModel) error {
	if len(fm.printOnExit) > 0 {
		fmt.Println(strings.Join(fm.printOnExit, "\n"))
	}
	if fm.linkTarget != nil {
		return dispatchUILink(ctx, fm.linkTarget)
	}
	if fm.launchUIId != "" && fm.launchUIHandlerFn != "" {
		ctx.Id = fm.launchUIId
		// The handler (e.g. getPipelineLogHandler) branches on --ui itself; this ctx may be
		// a picker-scoped ctx built for the "list" side that never had --ui set on it.
		if ctx.FlagValues == nil {
			ctx.FlagValues = map[string]any{}
		}
		ctx.FlagValues["ui"] = true
		return ctx.Resolver.RunUIHandler(ctx, fm.launchUIHandlerFn)
	}
	return nil
}

// RunUIDetailForGet opens the detail-only overlay (Case 4): no browse table
// underneath, rendered at the noun's default text entry, for a get command
// whose id is already fully specified and whose noun declares ui_commands.
func RunUIDetailForGet(ctx *cmdctx.Ctx, cs *spec.CommandSpec) error {
	if !console.IsBothTTY() {
		return fmt.Errorf("--ui requires an interactive terminal (TTY)")
	}
	nd := ctx.Resolver.GetNoun(cs.Noun)
	if nd == nil || len(nd.UICommands) == 0 {
		return fmt.Errorf("noun %q declares no ui_commands", cs.Noun)
	}
	defaultKey := defaultTextKey(nd.UICommands)
	if defaultKey == "" {
		return fmt.Errorf("noun %q: ui_commands has no default text entry", cs.Noun)
	}

	termWidth, termHeight := 120, 30
	if w, h, err := termSize(); err == nil {
		termWidth, termHeight = w, h
	}

	m := newUIDetailModel(ctx, cs, ctx.Id, nd.UICommands, defaultKey, termWidth, termHeight)
	p := tea.NewProgram(m)
	finalModel, err := p.Run()
	if err != nil {
		return err
	}
	fm, ok := finalModel.(uiTableModel)
	if !ok {
		return nil
	}
	return finishUIExit(ctx, fm)
}

// dispatchUILink follows a link-type ui_commands entry once the overlay quits:
// a "list" target opens that noun's browse overlay scoped to the current id as
// parent; a "get" target opens Case 4's detail-only overlay directly on the id.
func dispatchUILink(ctx *cmdctx.Ctx, lt *uiLinkTarget) error {
	targetCs := ctx.Resolver.GetSpec(lt.verb, lt.noun)
	if targetCs == nil {
		return fmt.Errorf("ui_commands link target %s %q not found", lt.verb, lt.noun)
	}
	switch lt.verb {
	case VerbList:
		if targetCs.Endpoint == nil {
			return fmt.Errorf("ui_commands link target %s %q has no endpoint", lt.verb, lt.noun)
		}
		listCtx := buildPickerCtx(ctx, targetCs)
		listCtx.ParentId = lt.id
		return RunUITable(listCtx, targetCs.Endpoint)
	case VerbGet:
		getCtx := buildDetailCtx(ctx, targetCs, lt.id)
		return RunUIDetailForGet(getCtx, targetCs)
	default:
		return fmt.Errorf("ui_commands link target %s %q: unsupported verb", lt.verb, lt.noun)
	}
}
