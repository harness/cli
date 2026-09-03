// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/harness/cli/pkg/auth"
	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/hbase"
	"github.com/harness/cli/pkg/spec"
)

// timeoutGracePeriod is the window given for in-flight work to finish after
// the timeout fires before the process is hard-killed with exit code 124.
const timeoutGracePeriod = time.Second

// completionTimeout is the default timeout for completion requests.
const completionTimeout = 5.0

// runTimeout sleeps for secs seconds, cancels the context with a timeout cause,
// then hard-exits with code 124 after timeoutGracePeriod. Intended to run in a goroutine.
func runTimeout(secs float64, cancel context.CancelCauseFunc) {
	time.Sleep(time.Duration(float64(time.Second) * secs))
	cancel(&cmdctx.TimeoutError{Secs: secs})
	time.Sleep(timeoutGracePeriod)
	os.Exit(hbase.TimeoutExitCode)
}

// parseScopePrefix inspects a raw id/parentId arg and returns the stripped value
// and the detected scope level. For list verbs, bare "account" / "org" are valid
// sentinels (returns "", level). For all other verbs a prefix is required to set
// a non-default level — bare "account"/"org" are treated as literal ids.
func parseScopePrefix(raw string, isList bool) (stripped string, level string) {
	if strings.HasPrefix(raw, "account.") {
		return strings.TrimPrefix(raw, "account."), "account"
	}
	if strings.HasPrefix(raw, "org.") {
		return strings.TrimPrefix(raw, "org."), "org"
	}
	if isList && raw == "account" {
		return "", "account"
	}
	if isList && raw == "org" {
		return "", "org"
	}
	return raw, "project"
}

// buildCompletionCtx constructs the Ctx needed for completion handlers.
// It resolves auth from the command's --profile/--org/--project flags.
// parentId is optional — pass "" when not applicable.
func (r *Registry) buildCompletionCtx(cmd *cobra.Command, verb, noun, parentId string) (*cmdctx.Ctx, error) {
	profileFlag, _ := cmd.Flags().GetString("profile")
	orgFlag, _ := cmd.Flags().GetString("org")
	projectFlag, _ := cmd.Flags().GetString("project")
	resolved, err := auth.ResolveWithOverrides(profileFlag, orgFlag, projectFlag)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	go runTimeout(completionTimeout, cancel)

	level := ""
	nd := r.GetNoun(noun)
	if nd != nil && nd.MultiLevel {
		if parentId != "" {
			parentId, level = parseScopePrefix(parentId, true)
		}
		if levelFlag, _ := cmd.Flags().GetString("level"); levelFlag != "" {
			if level != "" && level != "project" && level != levelFlag {
				// prefix and --level disagree — return an error so callers can bail on completions
				return nil, fmt.Errorf("--level %q conflicts with %q prefix", levelFlag, level)
			}
			level = levelFlag
		}
	}

	return &cmdctx.Ctx{
		Context:      ctx,
		CancelFn:     cancel,
		Verb:         verb,
		Noun:         noun,
		ParentId:     parentId,
		Level:        level,
		Auth:         resolved,
		Resolver:     r,
		IsCompletion: true,
	}, nil
}

// buildCtx constructs a Ctx from a cobra command, resolving auth and global flags.
func buildCtx(cmd *cobra.Command, cs *spec.CommandSpec, args []string, r *Registry) (*cmdctx.Ctx, error) {
	formatFlag, _ := cmd.Flags().GetString("format")
	jsonFlag, _ := cmd.Flags().GetBool("json")
	yamlFlag, _ := cmd.Flags().GetBool("yaml")
	if jsonFlag && yamlFlag {
		return nil, fmt.Errorf("--json and --yaml are mutually exclusive")
	}
	if (jsonFlag || yamlFlag) && formatFlag != "" {
		return nil, fmt.Errorf("--json/--yaml and --format are mutually exclusive")
	}
	if jsonFlag {
		formatFlag = "json"
	}
	if yamlFlag {
		formatFlag = "yaml"
	}
	columnsFlag, _ := cmd.Flags().GetString("columns")
	fieldsFlag, _ := cmd.Flags().GetString("fields")
	if fieldsFlag != "" && (jsonFlag || yamlFlag) {
		return nil, fmt.Errorf("--fields and --json/--yaml are mutually exclusive")
	}
	if fieldsFlag != "" && formatFlag != "" {
		return nil, fmt.Errorf("--fields and --format are mutually exclusive")
	}
	noHeaders, _ := cmd.Flags().GetBool("no-headers")
	outFile, _ := cmd.Flags().GetString("out")
	rawFlag, _ := cmd.Flags().GetBool("raw")

	timeoutSecs, _ := cmd.Flags().GetFloat64("timeout")
	if timeoutSecs < 0 {
		return nil, fmt.Errorf("--timeout must be >= 0 (got %g)", timeoutSecs)
	}

	goCtx, cancel := context.WithCancelCause(context.Background())
	if timeoutSecs > 0 {
		go runTimeout(timeoutSecs, cancel)
	}
	ctx := &cmdctx.Ctx{
		Context:     goCtx,
		CancelFn:    cancel,
		Verb:        cs.Verb,
		VerbHandler: cs.VerbHandler,
		Noun:        cs.Noun,
		FieldsNoun:  cs.FieldsNoun,
		IsPty:       term.IsTerminal(int(os.Stdout.Fd())),
		FormatFlags: cmdctx.FormatFlags{
			Format:    formatFlag,
			Columns:   columnsFlag,
			Fields:    fieldsFlag,
			NoHeaders: noHeaders,
			OutFile:   outFile,
			Raw:       rawFlag,
		},
	}
	listFields, _ := cmd.Flags().GetBool("list-fields")
	listColumns, _ := cmd.Flags().GetBool("list-columns")
	uiFlag, _ := cmd.Flags().GetBool("ui")
	skipIdCheck := listFields || listColumns || uiFlag

	idLabel := cs.IdLabel
	if idLabel == "" {
		idLabel = "<id>"
	}
	vspec := verbRegistry[cs.Verb]
	if (cs.Verb == VerbGet || cs.Verb == VerbList) && len(args) > 1 {
		return nil, fmt.Errorf("unexpected argument %q%s", args[1], cs.UsageLine())
	}
	nd := r.GetNoun(cs.Noun)
	// consumedIdArg tracks whether args[0] was actually assigned to ctx.Id
	// below, so the HasArgs/Set stripping logic further down only strips it
	// when it's truly a leftover id, not a native/passthrough arg.
	consumedIdArg := false
	if vspec.NounPair {
		if len(args) > 0 {
			return nil, fmt.Errorf("%s %s does not take a positional argument; use --from/--to%s", cs.Verb, cs.FullNoun(), cs.UsageLine())
		}
		ctx.MigrateFrom, _ = cmd.Flags().GetString("from")
		ctx.MigrateTo, _ = cmd.Flags().GetString("to")
		if cs.MigrateFrom.EffectivePresence() == spec.MigratePresenceRequired && ctx.MigrateFrom == "" {
			return nil, fmt.Errorf("flag --from is required")
		}
		if cs.MigrateTo.EffectivePresence() == spec.MigratePresenceRequired && ctx.MigrateTo == "" {
			return nil, fmt.Errorf("flag --to is required")
		}
	} else if vspec.RequiresId && !cs.NoId {
		if len(args) == 0 && !skipIdCheck {
			return nil, fmt.Errorf("%s %s requires a positional %s argument%s", cs.Verb, cs.Noun, idLabel, cs.UsageLine())
		}
		if len(args) > 0 {
			ctx.Id = args[0]
			consumedIdArg = true
		}
	} else if vspec.AllowsId || cs.AllowsId {
		// The id is optional here, so args[0] is only really the id if it
		// precedes a "--" (or there's no "--" at all) — cs.HasArgs commands
		// pass native/passthrough tokens after "--", which can look like a
		// flag (e.g. "-r") without being one. ArgsLenAtDash is 0 when "--" is
		// the very first token (no id given) and -1 when there's no "--".
		idGiven := len(args) > 0 && cmd.Flags().ArgsLenAtDash() != 0
		if cs.RequiresId && !idGiven && !skipIdCheck {
			return nil, fmt.Errorf("%s %s requires a positional %s argument%s", cs.Verb, cs.Noun, idLabel, cs.UsageLine())
		}
		if idGiven {
			ctx.Id = args[0]
			consumedIdArg = true
		}
	} else if vspec.AllowsParentId {
		if len(args) > 0 {
			ctx.ParentId = args[0]
		} else if cs.RequiresParentId && !skipIdCheck {
			label := cs.ParentIdLabel
			if label == "" {
				label = "<parentid>"
			}
			return nil, fmt.Errorf("%s %s requires a positional %s argument%s", cs.Verb, cs.Noun, label, cs.UsageLine())
		}
	}
	if nd != nil && nd.MultiLevel {
		if vspec.AllowsParentId && ctx.ParentId != "" {
			ctx.ParentId, ctx.Level = parseScopePrefix(ctx.ParentId, true)
		} else if ctx.Id != "" {
			ctx.Id, ctx.Level = parseScopePrefix(ctx.Id, false)
		}
		if levelFlag, _ := cmd.Flags().GetString("level"); levelFlag != "" {
			valid := false
			for _, v := range specLevelValues {
				if levelFlag == v {
					valid = true
					break
				}
			}
			if !valid {
				return nil, fmt.Errorf("invalid --level %q: must be one of %s", levelFlag, strings.Join(specLevelValues, ", "))
			}
			if ctx.Level != "" && ctx.Level != "project" && ctx.Level != levelFlag {
				return nil, fmt.Errorf("--level %q conflicts with %q prefix on id", levelFlag, ctx.Level)
			}
			ctx.Level = levelFlag
		}
	}
	if err := validateIdParts(cs, vspec, ctx); err != nil {
		return nil, err
	}
	if !cs.NoAuth {
		profileFlag, _ := cmd.Flags().GetString("profile")
		orgFlag, _ := cmd.Flags().GetString("org")
		projectFlag, _ := cmd.Flags().GetString("project")
		resolved, err := auth.ResolveWithOverrides(profileFlag, orgFlag, projectFlag)
		if err != nil {
			return nil, err
		}
		ctx.Auth = resolved
	}
	if cs.HasArgs {
		extra := args
		if consumedIdArg {
			extra = args[1:]
		}
		ctx.Args = extra
	}
	if cs.BuiltinFlags.Set {
		setVals, _ := cmd.Flags().GetStringArray("set")
		// positional args after the id are also treated as key=value pairs
		positional := args
		if consumedIdArg {
			positional = args[1:]
		}
		all := append(setVals, positional...)
		if len(all) > 0 {
			ctx.SetArgs = make(map[string]string, len(all))
			for _, kv := range all {
				k, v, ok := strings.Cut(kv, "=")
				if !ok {
					return nil, fmt.Errorf("invalid value %q: expected key=value format", kv)
				}
				ctx.SetArgs[k] = v
			}
		}
	}
	if cs.BuiltinFlags.Del {
		delVals, _ := cmd.Flags().GetStringArray("del")
		if len(delVals) > 0 {
			ctx.DelArgs = delVals
		}
	}
	ctx.FlagValues = buildFlagValues(cmd.Flags(), cs)
	ctx.Resolver = r
	if err := resolveFlagValues(ctx, cs); err != nil {
		return nil, err
	}
	for _, f := range cs.Flags {
		if f.Required && cmdctx.GetString(ctx.FlagValues, f.Name) == "" {
			if len(f.CompletionValues) > 0 {
				return nil, fmt.Errorf("flag --%s is required (%s)", f.Name, strings.Join(f.CompletionValues, ", "))
			}
			return nil, fmt.Errorf("flag --%s is required", f.Name)
		}
	}
	return ctx, nil
}

// buildDetailCtx constructs a minimal Ctx for a get-by-id drilldown from inside
// the TUI. It copies auth and scope from the parent list ctx, overrides the verb/noun
// to "get", and injects the resolved id.
func buildDetailCtx(parent *cmdctx.Ctx, cs *spec.CommandSpec, id string) *cmdctx.Ctx {
	goCtx, cancel := context.WithCancelCause(parent.Context)
	ctx := &cmdctx.Ctx{
		Context:     goCtx,
		CancelFn:    cancel,
		Auth:        parent.Auth,
		Verb:        cs.Verb,
		VerbHandler: cs.VerbHandler,
		Noun:        cs.Noun,
		FieldsNoun:  cs.FieldsNoun,
		Id:          id,
		Level:       parent.Level,
		IsPty:       parent.IsPty,
		Resolver:    parent.Resolver,
		FormatFlags: cmdctx.FormatFlags{Format: "text"},
		FlagValues:  map[string]any{},
		UIHistory:   parent.UIHistory,
	}
	// Endpoint path templates split ctx.Id into idParts on the fly (see exprenv.Make), but
	// workflow handlers that read the ctx.IdParts struct field directly (e.g. a multi-part
	// id_parts target resolved via item_fn) need it populated here too.
	if cs.IdParts > 1 {
		ctx.IdParts = strings.SplitN(id, "/", cs.IdParts)
	}
	return ctx
}

// buildLinkCtx constructs the Ctx for a replayed UILink — either a forward hop
// (link/up/view) or a popped History entry. Unlike buildDetailCtx/buildPickerCtx, which
// inherit the caller's already-resolved Auth verbatim, this re-resolves auth from the
// Link's raw profile/org/project strings, so a Link that captured a different scope
// replays with that scope instead of whatever happens to be live in the caller's ctx.
func buildLinkCtx(ctx *cmdctx.Ctx, link *cmdctx.UILink, targetCs *spec.CommandSpec) (*cmdctx.Ctx, error) {
	var resolved *auth.ResolvedAuth
	if !targetCs.NoAuth {
		var err error
		resolved, err = auth.ResolveWithOverrides(link.Profile, link.Org, link.Project)
		if err != nil {
			return nil, err
		}
	}
	fv := link.FlagValues
	if fv == nil {
		fv = map[string]any{}
	}
	goCtx, cancel := context.WithCancelCause(ctx.Context)
	newCtx := &cmdctx.Ctx{
		Context:     goCtx,
		CancelFn:    cancel,
		Auth:        resolved,
		Verb:        targetCs.Verb,
		VerbHandler: targetCs.VerbHandler,
		Noun:        targetCs.Noun,
		FieldsNoun:  targetCs.FieldsNoun,
		Level:       link.Level,
		IsPty:       ctx.IsPty,
		Resolver:    ctx.Resolver,
		FormatFlags: cmdctx.FormatFlags{Format: "text"},
		FlagValues:  fv,
		UIHistory:   ctx.UIHistory,
	}
	if link.Screen == cmdctx.ScreenDetailForGet {
		newCtx.Id = link.Id
		if targetCs.IdParts > 1 {
			newCtx.IdParts = strings.SplitN(link.Id, "/", targetCs.IdParts)
		}
	} else {
		newCtx.ParentId = link.Id
	}
	return newCtx, nil
}

// resolveFlagValues runs any flag_resolve_fn declared on spec flags, overwriting
// the raw string value in ctx.FlagValues with the resolved result. Skips flags
// whose value is empty. Called after buildFlagValues and auth resolution.
//
// A resolver may also return Defaults for sibling flags; each is applied only
// if that flag is currently unset, so an explicitly-set flag always wins.
func resolveFlagValues(ctx *cmdctx.Ctx, cs *spec.CommandSpec) error {
	for _, f := range cs.Flags {
		if f.FlagResolveFn == "" {
			continue
		}
		raw, _ := ctx.FlagValues[f.Name].(string)
		if raw == "" {
			continue
		}
		fn := ctx.Resolver.ResolveFlagResolveFn(f.FlagResolveFn)
		if fn == nil {
			return fmt.Errorf("flag_resolve_fn %q not registered", f.FlagResolveFn)
		}
		result, err := fn(ctx, raw)
		if err != nil {
			return fmt.Errorf("--%s: %w", f.Name, err)
		}
		ctx.FlagValues[f.Name] = result.Value
		for name, def := range result.Defaults {
			if existing, _ := ctx.FlagValues[name].(string); existing == "" {
				ctx.FlagValues[name] = def
			}
		}
	}
	return nil
}

func validateIdParts(cs *spec.CommandSpec, vspec VerbSpec, ctx *cmdctx.Ctx) error {
	val, label := ctx.Id, cs.IdLabel
	if vspec.AllowsParentId {
		val = ctx.ParentId
		if cs.ParentIdLabel != "" {
			label = "<" + cs.ParentIdLabel + ">"
		}
	}
	if label == "" {
		label = "<id>"
	}
	if val == "" || cs.IdAllowSlash {
		return nil
	}
	allowed := max(cs.IdParts-1, 0)
	if got := strings.Count(val, "/"); got > allowed {
		if cs.IdParts > 1 {
			return fmt.Errorf("expected %s with exactly %d parts separated by '/', got %q", label, cs.IdParts, val)
		}
		return fmt.Errorf("%s %s: %s must not contain '/' (got %q)", cs.Verb, cs.Noun, label, val)
	}
	if cs.IdParts > 1 {
		ctx.IdParts = strings.SplitN(val, "/", cs.IdParts)
	}
	return nil
}
