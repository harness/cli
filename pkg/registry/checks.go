// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"errors"
	"fmt"
	"strings"

	"github.com/harness/cli/pkg/spec"
)

// CheckFunctions verifies that every function reference in all registered specs
// resolves to a registered function. Returns an error listing all unresolved references.
func (r *Registry) CheckFunctions() error {
	errs := append([]string(nil), r.initErrs...)
	for _, specs := range r.specs {
		for _, cs := range specs {
			errs = append(errs, r.checkFunctionsSpec(cs)...)
		}
	}
	for noun, nd := range r.nouns {
		errs = append(errs, r.checkUICommands(noun, nd)...)
	}
	if len(errs) > 0 {
		return errors.New("registry errors:\n  " + strings.Join(errs, "\n  "))
	}
	return nil
}

// reservedUIKeys are hardcoded to scroll/quit/print handling in the detail
// overlay's key switch (see uitableview.go) and never reach ui_commands
// dispatch, so a spec binding one of them would silently never fire.
var reservedUIKeys = map[string]bool{
	"p": true, "q": true, "ctrl+c": true, "esc": true, "backspace": true,
	"up": true, "down": true, "k": true, "j": true, "pgup": true, "pgdown": true,
}

// checkUICommands validates a noun's ui_commands list: unique non-reserved keys,
// exactly one default text entry, and that every text/link/view target resolves.
func (r *Registry) checkUICommands(noun string, nd spec.NounDef) []string {
	if len(nd.UICommands) == 0 {
		return nil
	}
	var errs []string
	seenKeys := map[string]bool{}
	defaultCount := 0
	for _, uc := range nd.UICommands {
		if uc.Key == "" {
			errs = append(errs, fmt.Sprintf("noun %q: ui_commands entry missing key", noun))
		} else if reservedUIKeys[uc.Key] {
			errs = append(errs, fmt.Sprintf("noun %q: ui_commands key %q is reserved", noun, uc.Key))
		} else if seenKeys[uc.Key] {
			errs = append(errs, fmt.Sprintf("noun %q: ui_commands key %q is duplicated", noun, uc.Key))
		}
		seenKeys[uc.Key] = true

		switch uc.UICommandType {
		case spec.UICommandText:
			if uc.Default {
				defaultCount++
			}
			if r.GetSpec(VerbGet, uc.Noun) == nil {
				errs = append(errs, fmt.Sprintf("noun %q: ui_commands text entry %q: noun %q does not resolve via get", noun, uc.Key, uc.Noun))
			}
		case spec.UICommandLink:
			if uc.Default {
				errs = append(errs, fmt.Sprintf("noun %q: ui_commands link entry %q: default is only allowed on text entries", noun, uc.Key))
			}
			if uc.Verb != VerbList && uc.Verb != VerbGet {
				errs = append(errs, fmt.Sprintf("noun %q: ui_commands link entry %q: verb must be %q or %q", noun, uc.Key, VerbList, VerbGet))
			} else if r.GetSpec(uc.Verb, uc.Noun) == nil {
				errs = append(errs, fmt.Sprintf("noun %q: ui_commands link entry %q: %s %q does not resolve", noun, uc.Key, uc.Verb, uc.Noun))
			}
		case spec.UICommandView:
			if uc.Default {
				errs = append(errs, fmt.Sprintf("noun %q: ui_commands view entry %q: default is only allowed on text entries", noun, uc.Key))
			}
			if _, ok := r.workflows[uc.UIHandlerFn]; !ok {
				errs = append(errs, fmt.Sprintf("noun %q: ui_commands view entry %q: ui_handler_fn %q not registered", noun, uc.Key, uc.UIHandlerFn))
			}
		case spec.UICommandUp:
			if uc.Default {
				errs = append(errs, fmt.Sprintf("noun %q: ui_commands up entry %q: default is only allowed on text entries", noun, uc.Key))
			}
			verb := uc.Verb
			if verb == "" {
				verb = VerbGet
			}
			if verb != VerbList && verb != VerbGet {
				errs = append(errs, fmt.Sprintf("noun %q: ui_commands up entry %q: verb must be %q or %q", noun, uc.Key, VerbList, VerbGet))
			} else if targetCs := r.GetSpec(verb, uc.Noun); targetCs == nil {
				errs = append(errs, fmt.Sprintf("noun %q: ui_commands up entry %q: %s %q does not resolve", noun, uc.Key, verb, uc.Noun))
			} else if uc.UpIdExpr == "" && upTargetRequiresId(verb, targetCs) {
				errs = append(errs, fmt.Sprintf("noun %q: ui_commands up entry %q: up_id_expr is required (%s %q requires an id)", noun, uc.Key, verb, uc.Noun))
			}
		default:
			errs = append(errs, fmt.Sprintf("noun %q: ui_commands entry %q: invalid ui_command_type %q", noun, uc.Key, uc.UICommandType))
		}
	}
	if defaultCount != 1 {
		errs = append(errs, fmt.Sprintf("noun %q: ui_commands requires exactly one default text entry, found %d", noun, defaultCount))
	}
	return errs
}

// upTargetRequiresId reports whether an up entry's resolved target command
// actually needs an id to run: get commands need ctx.Id unless opted out via
// NoId, list commands only need ctx.ParentId when requires_parentid is set.
func upTargetRequiresId(verb string, cs *spec.CommandSpec) bool {
	switch verb {
	case VerbGet:
		return verbRegistry[VerbGet].RequiresId && !cs.NoId
	case VerbList:
		return cs.RequiresParentId
	default:
		return false
	}
}

func (r *Registry) checkFunctionsSpec(cs *spec.CommandSpec) []string {
	if cs.DevOnly || cs.External {
		return nil
	}
	var errs []string
	if cs.WorkflowID != "" {
		if _, ok := r.workflows[cs.WorkflowID]; !ok {
			errs = append(errs, fmt.Sprintf("command %q: workflow_id %q not registered", cs.Command, cs.WorkflowID))
		}
	}
	if cs.ItemFn != "" {
		if _, ok := r.itemFns[cs.ItemFn]; !ok {
			errs = append(errs, fmt.Sprintf("command %q: item_fn %q not registered", cs.Command, cs.ItemFn))
		}
	}
	if cs.Endpoint != nil {
		if cs.Endpoint.TextFormatter != "" {
			if _, ok := r.textFormatters[cs.Endpoint.TextFormatter]; !ok {
				errs = append(errs, fmt.Sprintf("command %q: text_formatter %q not registered", cs.Command, cs.Endpoint.TextFormatter))
			}
		}
		if cs.Endpoint.BodyFn != "" {
			if _, ok := r.bodyFns[cs.Endpoint.BodyFn]; !ok {
				errs = append(errs, fmt.Sprintf("command %q: body_fn %q not registered", cs.Command, cs.Endpoint.BodyFn))
			}
		}
		if cs.Endpoint.QueryParamsFn != "" {
			if _, ok := r.queryParamsFns[cs.Endpoint.QueryParamsFn]; !ok {
				errs = append(errs, fmt.Sprintf("command %q: query_params_fn %q not registered", cs.Command, cs.Endpoint.QueryParamsFn))
			}
		}
		if cs.Endpoint.ListTransformFn != "" {
			if _, ok := r.listTransformFns[cs.Endpoint.ListTransformFn]; !ok {
				errs = append(errs, fmt.Sprintf("command %q: list_transform_fn %q not registered", cs.Command, cs.Endpoint.ListTransformFn))
			}
		}
	}
	if cs.FollowFn != "" {
		if _, ok := r.followFns[cs.FollowFn]; !ok {
			errs = append(errs, fmt.Sprintf("command %q: follow_fn %q not registered", cs.Command, cs.FollowFn))
		}
	}
	for _, f := range cs.Flags {
		if f.CompletionFn != "" {
			if _, ok := r.flagCompletionFns[f.CompletionFn]; !ok {
				errs = append(errs, fmt.Sprintf("command %q: flag %q completion_fn %q not registered", cs.Command, f.Name, f.CompletionFn))
			}
		}
		if f.FlagResolveFn != "" {
			if _, ok := r.flagResolveFns[f.FlagResolveFn]; !ok {
				errs = append(errs, fmt.Sprintf("command %q: flag %q flag_resolve_fn %q not registered", cs.Command, f.Name, f.FlagResolveFn))
			}
		}
	}
	return errs
}

// CheckWarnings returns non-fatal spec warnings. Checks list endpoint commands
// and that every module has a help_text defined.
func (r *Registry) CheckWarnings() []string {
	var warns []string
	for _, specs := range r.specs {
		for _, cs := range specs {
			warns = append(warns, warnSpec(cs)...)
		}
	}
	for _, m := range r.moduleMetas {
		if m.HelpText == "" {
			warns = append(warns, fmt.Sprintf("module %q: missing help_text", m.Name))
		}
	}
	return warns
}

func warnSpec(cs *spec.CommandSpec) []string {
	if cs.DevOnly || cs.External {
		return nil
	}
	if cs.VerbHandler != VerbList {
		return nil
	}
	if cs.HandlerType != spec.HandlerEndpoint || cs.Endpoint == nil {
		return nil
	}
	var warns []string
	if cs.Endpoint.Paging == nil || cs.Endpoint.Paging.PagingStrategy == "" {
		warns = append(warns, fmt.Sprintf("command %q: list endpoint is missing paging_strategy", cs.Command))
	}
	if cs.Endpoint.GetIdExpr == "" {
		warns = append(warns, fmt.Sprintf("command %q: list endpoint is missing get_id_expr (set \"-\" to suppress)", cs.Command))
	}
	return warns
}

func validateSpec(cs *spec.CommandSpec, vs VerbSpec) error {
	if err := validateVerbNounShape(cs, vs); err != nil {
		return err
	}
	if err := validateConfirmMode(cs); err != nil {
		return err
	}
	if err := validateEndpointConstraints(cs); err != nil {
		return err
	}
	if err := validateNounPairConstraints(cs, vs); err != nil {
		return err
	}
	return nil
}

// validateNounPairConstraints enforces the pair-verb shape (see VerbSpec.NounPair):
// the command must declare noun_to (not noun_variant — there is no base+variant here,
// just two distinct nouns), and dispatch must be a workflow — a pair verb has no single
// endpoint to bind to.
func validateNounPairConstraints(cs *spec.CommandSpec, vs VerbSpec) error {
	if !vs.NounPair {
		if cs.MigrateFrom != nil || cs.MigrateTo != nil {
			return fmt.Errorf("command %q: migrate_from/migrate_to are only valid on pair verbs (%s is not)", cs.Command, cs.Verb)
		}
		return nil
	}
	for _, mf := range []struct {
		name string
		spec *spec.MigrateFlag
	}{{"migrate_from", cs.MigrateFrom}, {"migrate_to", cs.MigrateTo}} {
		switch mf.spec.EffectivePresence() {
		case spec.MigratePresenceRequired, spec.MigratePresenceOptional:
		case spec.MigratePresenceNone:
			if mf.spec.Label != "" || mf.spec.IdLabel != "" {
				return fmt.Errorf("command %q: %s declares label/id_label with presence: none (the flag is not registered)", cs.Command, mf.name)
			}
		default:
			return fmt.Errorf("command %q: %s presence %q must be one of required, optional, none", cs.Command, mf.name, mf.spec.Presence)
		}
	}
	if cs.NounTo == "" {
		return fmt.Errorf("command %q: %s command must declare noun_to (noun2 in \"noun1:noun2\")", cs.Command, cs.Verb)
	}
	if cs.NounVariant != "" {
		return fmt.Errorf("command %q: %s command must not declare noun_variant (use noun_to)", cs.Command, cs.Verb)
	}
	if cs.HandlerType != spec.HandlerWorkflow {
		return fmt.Errorf("command %q: %s command must use handler_type: workflow (no endpoint)", cs.Command, cs.Verb)
	}
	if cs.Endpoint != nil {
		return fmt.Errorf("command %q: %s command must not declare an endpoint", cs.Command, cs.Verb)
	}
	return nil
}

// validateVerbNounShape checks command naming, verb kind / noun presence, and id_parts.
func validateVerbNounShape(cs *spec.CommandSpec, vs VerbSpec) error {
	wantCommand := strings.TrimSpace(cs.Verb + " " + cs.FullNoun())
	if cs.Command != wantCommand {
		return fmt.Errorf("command %q must equal %q (verb+noun)", cs.Command, wantCommand)
	}
	if vs.Kind == VerbKindLeaf && cs.Noun != "" {
		return fmt.Errorf("leaf verb %q cannot have a noun", cs.Verb)
	}
	if vs.Kind == VerbKindGroup && cs.Noun == "" {
		return fmt.Errorf("group verb %q requires a noun", cs.Verb)
	}
	if vs.Kind == VerbKindCore && cs.Noun == "" {
		return fmt.Errorf("core verb %q requires a noun", cs.Verb)
	}
	if cs.IdParts < 0 || cs.IdParts > 3 {
		return fmt.Errorf("command %q: id_parts must be between 1 and 3, got %d", cs.Command, cs.IdParts)
	}
	return nil
}

// validateConfirmMode checks that confirm_mode is a known value and isn't set on read-only verbs.
func validateConfirmMode(cs *spec.CommandSpec) error {
	switch cs.ConfirmMode {
	case spec.ConfirmNone, spec.ConfirmPrompt, spec.ConfirmID:
		// valid
	default:
		return fmt.Errorf("command %q: invalid confirm_mode %q (must be prompt or confirm_id)", cs.Command, cs.ConfirmMode)
	}
	if (cs.VerbHandler == VerbList || cs.VerbHandler == VerbGet) && cs.ConfirmMode != spec.ConfirmNone {
		return fmt.Errorf("command %q: confirm_mode is not supported on %s commands", cs.Command, cs.VerbHandler)
	}
	return nil
}

// validateEndpointConstraints checks list/get response expressions, paging placement,
// body method compatibility, and file_body values.
func validateEndpointConstraints(cs *spec.CommandSpec) error {
	if cs.HandlerType != spec.HandlerEndpoint || cs.Endpoint == nil {
		return nil
	}
	ep := cs.Endpoint
	if ep.Paging != nil && cs.VerbHandler != VerbList {
		return fmt.Errorf("command %q: paging is only allowed on list verbs", cs.Command)
	}
	if ep.ItemsExpr != "" && cs.VerbHandler != VerbList {
		return fmt.Errorf("command %q: items_expr is only allowed on list verbs", cs.Command)
	}
	if cs.VerbHandler == VerbList && ep.ItemsExpr == "" {
		return fmt.Errorf("list endpoint %q requires items_expr (use \"it\" for bare arrays)", cs.FullNoun())
	}
	if cs.VerbHandler == VerbGet && ep.ItemExpr == "" {
		return fmt.Errorf("get endpoint %q requires item_expr (use \"it\" for bare item responses)", cs.FullNoun())
	}
	if ep.ListTransformFn != "" && cs.VerbHandler == VerbList {
		return fmt.Errorf("command %q: list_transform_fn is not allowed on list verbs (use items_expr instead)", cs.Command)
	}
	if ep.ListTransformFn != "" && ep.FieldExtract != "" {
		return fmt.Errorf("command %q: list_transform_fn and field_extract are mutually exclusive", cs.Command)
	}
	if ep.ListTransformFn != "" && ep.TextFormatter != "" {
		return fmt.Errorf("command %q: list_transform_fn and text_formatter are mutually exclusive", cs.Command)
	}
	if ep.Paging != nil {
		if err := validatePaging(cs.Command, ep.Paging); err != nil {
			return err
		}
	}
	if len(ep.BodyParams) > 0 || ep.BodyFn != "" {
		method := ep.Method
		if method == "" {
			method = "GET"
		}
		switch method {
		case "POST", "PUT", "PATCH", "DELETE":
			// body allowed
		default:
			return fmt.Errorf("command %q: body_params/body_fn not allowed on %s requests", cs.Command, method)
		}
	}
	switch ep.FileBody {
	case spec.FileBodyNone, spec.FileBodyOptional, spec.FileBodyRequired:
		// valid
	default:
		return fmt.Errorf("command %q: invalid file_body %q (must be \"optional\" or \"required\")", cs.Command, ep.FileBody)
	}
	switch ep.ContentType {
	case "", "application/json", "application/merge-patch+json", "application/yaml":
		// valid
	default:
		return fmt.Errorf("command %q: invalid content_type %q (must be \"application/json\", \"application/merge-patch+json\", or \"application/yaml\")", cs.Command, ep.ContentType)
	}
	if ep.ContentType != "" && ep.FileBody == spec.FileBodyNone {
		return fmt.Errorf("command %q: content_type requires file_body to be set", cs.Command)
	}
	switch ep.FileBodyContentType {
	case "", "application/json", "application/merge-patch+json", "application/yaml":
		// valid
	default:
		return fmt.Errorf("command %q: invalid file_body_content_type %q (must be \"application/json\", \"application/merge-patch+json\", or \"application/yaml\")", cs.Command, ep.FileBodyContentType)
	}
	if ep.FileBodyContentType != "" && ep.FileBody == spec.FileBodyNone {
		return fmt.Errorf("command %q: file_body_content_type requires file_body to be set", cs.Command)
	}
	if ep.FileBodyWrapAsString != "" && ep.FileBody == spec.FileBodyNone {
		return fmt.Errorf("command %q: file_body_wrap_as_string requires file_body to be set", cs.Command)
	}
	return nil
}

func validatePaging(command string, pg *spec.PagingSpec) error {
	switch pg.PagingStrategy {
	case spec.PagingStrategyPageIndex, spec.PagingStrategyPageHeader, spec.PagingStrategyOffsetLimit:
		// valid, fall through to server-paging checks below
	case spec.PagingStrategyFlatList:
		return nil
	default:
		return fmt.Errorf("command %q: unknown paging model %q", command, pg.PagingStrategy)
	}
	if pg.PageSizeMax <= 0 {
		return fmt.Errorf("command %q: paging requires page_size_max > 0", command)
	}
	if pg.PagingStrategy == spec.PagingStrategyPageIndex || pg.PagingStrategy == spec.PagingStrategyPageHeader {
		if pg.PageSizeDefault <= 0 {
			return fmt.Errorf("command %q: paging requires page_size_default > 0", command)
		}
		if pg.PageSizeMax < pg.PageSizeDefault {
			return fmt.Errorf("command %q: paging page_size_max (%d) must be >= page_size_default (%d)", command, pg.PageSizeMax, pg.PageSizeDefault)
		}
		if pg.PageIndexParam == "" {
			return fmt.Errorf("command %q: paging requires page_index_param", command)
		}
		if pg.PageSizeParam == "" {
			return fmt.Errorf("command %q: paging requires page_size_param", command)
		}
	}
	if pg.PagingStrategy == spec.PagingStrategyPageIndex && pg.TotalExpr == "" {
		return fmt.Errorf("command %q: page_index paging requires total_expr", command)
	}
	return nil
}
