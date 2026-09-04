// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"fmt"
	"strings"

	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/spec"
)

// RunUIPickerForGet intercepts a get command with --ui and, when ctx.Id isn't already
// fully specified, resolves it interactively following the command's completion spec
// exactly as tab-completion does.
//
// Any completion_seq steps before the last are resolved via quick select-and-continue
// pickers (RunUIPicker). The final step — the actual target noun — hands off to the
// same browse+detail TUI used by "list <noun> --ui" (RunUITableForGet), so picking an
// id there and printing/viewing it goes through the identical screen as browsing the
// list directly. That final call fully owns the outcome (print, launch the viewer, or
// quit with nothing done), so handled=true tells the caller not to do anything further.
//
// handled=false means ctx.Id was already fully specified and the caller should proceed
// with its normal (non-TUI) get flow.
//
// ID pre-fill rules:
//   - If ctx.Id is fully specified (last "/" segment non-empty, or no seq), handled=false.
//   - If ctx.Id ends with "/" (trailing slash), the parts before the slash are treated as done.
func RunUIPickerForGet(ctx *cmdctx.Ctx, cs *spec.CommandSpec) (bool, error) {
	switch {
	case len(cs.CompletionSeq) > 0:
		return runSeqPicker(ctx, cs)
	case cs.CompletionNoun != "":
		return runSimplePicker(ctx, cs)
	default:
		return false, fmt.Errorf("--ui requires completion_noun or completion_seq on %s %s", cs.Verb, cs.Noun)
	}
}

// runSimplePicker handles the single-step case (completion_noun).
func runSimplePicker(ctx *cmdctx.Ctx, cs *spec.CommandSpec) (bool, error) {
	if ctx.Id != "" {
		return false, nil
	}
	listCs := ctx.Resolver.GetSpec(VerbList, cs.CompletionNoun)
	if listCs == nil || listCs.Endpoint == nil {
		return false, fmt.Errorf("no list command found for completion_noun %q", cs.CompletionNoun)
	}
	pickerCtx := buildPickerCtx(ctx, listCs)
	return true, RunUITableForGet(pickerCtx, listCs.Endpoint, cs)
}

// runSeqPicker handles multi-step completion_seq.
func runSeqPicker(ctx *cmdctx.Ctx, cs *spec.CommandSpec) (bool, error) {
	steps := cs.CompletionSeq
	totalSteps := len(steps)

	// Parse already-provided parts from ctx.Id.
	// "mikereg/art/" → ["mikereg", "art"] (done), one step remaining.
	// "mikereg/art/v1" → ["mikereg", "art", "v1"] (all done, last non-empty).
	// "" → [] (all steps needed).
	doneParts, allDone := parseProvidedId(ctx.Id, totalSteps)
	if allDone {
		return false, nil
	}

	picked := make([]string, totalSteps)
	copy(picked, doneParts)

	for i, step := range steps {
		if i < len(doneParts) {
			continue // already have this part
		}
		listCs := ctx.Resolver.GetSpec(VerbList, step.CompletionNoun)
		if listCs == nil || listCs.Endpoint == nil {
			return false, fmt.Errorf("no list command found for completion_seq step %d noun %q", i, step.CompletionNoun)
		}
		pickerCtx := buildPickerCtx(ctx, listCs)
		// Pass already-picked parts as parentId so list endpoints that use
		// ctx.parentId / ctx.parentIdParts resolve correctly.
		pickerCtx.ParentId = strings.Join(picked[:i], "/")

		if i == totalSteps-1 {
			// Final step: browse+detail TUI, same as "list <noun> --ui".
			return true, RunUITableForGet(pickerCtx, listCs.Endpoint, cs)
		}

		noun := strings.ReplaceAll(step.CompletionNoun, "_", " ")
		title := fmt.Sprintf("pick %s  (%d of %d)", noun, i+1, totalSteps)
		verbNoun := cs.Verb + " " + strings.ReplaceAll(cs.Noun, "_", " ")
		donePrefix := strings.Join(picked[:i], "/")
		if donePrefix != "" {
			donePrefix += "/"
		}
		suffix := strings.Repeat("/…", totalSteps-1-i)
		preview := &PickerPreview{Verb: verbNoun, Done: donePrefix, Suffix: suffix}
		id, err := RunUIPicker(pickerCtx, listCs.Endpoint, title, preview)
		if err != nil {
			return false, err
		}
		picked[i] = id
	}

	// Unreachable when totalSteps > 0 (the loop always returns on the last step above).
	return false, fmt.Errorf("command %q: completion_seq must have at least one step", cs.Command)
}

// parseProvidedId splits ctx.Id into done parts and reports whether the id is
// fully specified (no more picker steps needed).
//
//   - ""              → ([], false)   — nothing provided
//   - "a/"            → (["a"], false) — trailing slash, one step remains
//   - "a/b"           → (["a","b"], true) — last segment non-empty, all done
//   - "a/b/"          → (["a","b"], false) — two done, one step remains
func parseProvidedId(id string, totalSteps int) (doneParts []string, allDone bool) {
	if id == "" {
		return nil, false
	}
	// If last char is not "/" the id is complete as-is.
	if !strings.HasSuffix(id, "/") {
		return nil, true
	}
	// Trailing slash: split and treat non-empty parts as done.
	parts := strings.Split(strings.TrimSuffix(id, "/"), "/")
	var done []string
	for _, p := range parts {
		if p != "" {
			done = append(done, p)
		}
	}
	if len(done) >= totalSteps {
		return nil, true
	}
	return done, false
}

// buildPickerCtx builds a list-scoped ctx from the get ctx, inheriting auth and level.
// If the list spec declares a --search flag, it is seeded into FlagValues so the picker
// TUI enables "/" search.
func buildPickerCtx(getCtx *cmdctx.Ctx, listCs *spec.CommandSpec) *cmdctx.Ctx {
	goCtx, cancel := getCtx.Context, getCtx.CancelFn
	fv := map[string]any{}
	for _, f := range listCs.Flags {
		if f.Name == "search" {
			fv["search"] = ""
			break
		}
	}
	return &cmdctx.Ctx{
		Context:     goCtx,
		CancelFn:    cancel,
		Auth:        getCtx.Auth,
		Verb:        listCs.Verb,
		VerbHandler: listCs.VerbHandler,
		Noun:        listCs.Noun,
		FieldsNoun:  listCs.FieldsNoun,
		Level:       getCtx.Level,
		IsPty:       getCtx.IsPty,
		Resolver:    getCtx.Resolver,
		FormatFlags: cmdctx.FormatFlags{},
		FlagValues:  fv,
		UIHistory:   getCtx.UIHistory,
	}
}
