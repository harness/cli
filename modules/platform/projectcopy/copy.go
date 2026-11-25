// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package projectcopy

import (
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/harness/cli/v3/pkg/cmdctx"
)

// row is one resource type in copy order. Parent is the noun whose copied
// identifiers this row is listed under. Mode splits a noun that has to be
// written in more than one pass.
type row struct {
	Noun   string
	Parent string
	Mode   string
}

// cdCore is the copy order. Secret-manager connectors are created before the
// reference secrets that name them. Everything else is one row.
var cdCore = []row{
	{Noun: "project"},
	{Noun: "connector", Mode: "secret-manager"},
	{Noun: "secret"},
	{Noun: "connector", Mode: "rest"},
	{Noun: "service"},
	{Noun: "environment"},
	{Noun: "infrastructure", Parent: "environment"},
	{Noun: "service_override", Parent: "environment"},
	{Noun: "template"},
	{Noun: "pipeline"},
	{Noun: "input_set", Parent: "pipeline"},
	{Noun: "trigger", Parent: "pipeline"},
	{Noun: "policy"},
	{Noun: "policy_set"},
	{Noun: "delegate"},
}

// Copy plans a same-org CD-core copy from scope.Source to scope.Destination.
// apply creates the destination project when it is missing and writes resources.
// Conflicting destination content aborts before any write.
func Copy(ctx *cmdctx.Ctx, scope Scope, apply bool) (Report, error) {
	if ctx == nil || ctx.Auth == nil || ctx.Resolver == nil {
		return NewCopyReport(scope, apply), errors.New("resolved authentication required")
	}
	return runCopy(scope, apply, newSpecGateway(ctx))
}

func runCopy(scope Scope, apply bool, gw Gateway) (Report, error) {
	c := &copier{
		gw: gw, scope: scope, apply: apply, report: NewCopyReport(scope, apply),
		selectors: map[string]struct{}{}, shared: map[string]struct{}{}, managers: map[string]bool{},
	}
	err := c.run()
	return c.report, err
}

type copier struct {
	gw        Gateway
	scope     Scope
	apply     bool
	report    Report
	selectors map[string]struct{}
	shared    map[string]struct{}
	managers  map[string]bool
	plans     []work
	conflict  bool
	blocked   bool
	skipped   bool
	pending   bool
}

type work struct {
	result Result
	write  func() error
}

func (c *copier) run() error {
	if err := c.scope.Validate(); err != nil {
		return err
	}
	for _, item := range cdCore {
		if err := c.plan(item); err != nil {
			return err
		}
	}
	return c.finish()
}

func (c *copier) plan(item row) error {
	switch item.Noun {
	case "project":
		return c.planProject()
	case "connector":
		if item.Mode == "secret-manager" {
			if err := c.scanSecretManagers(); err != nil {
				return err
			}
		}
		return c.planConnectors(item.Mode == "secret-manager")
	case "secret":
		return c.planSecrets()
	case "template":
		return c.planTemplates()
	case "delegate":
		return c.planDelegates()
	case "policy", "policy_set", "service_override":
		return c.forParents(item.Parent, func(parent string) error {
			return c.planObject(item.Noun, parent)
		})
	default:
		return c.forParents(item.Parent, func(parent string) error {
			return c.planYAML(item.Noun, parent)
		})
	}
}

func (c *copier) forParents(parentNoun string, fn func(string) error) error {
	parents := []string{""}
	if parentNoun != "" {
		parents = c.idsOf(parentNoun, ActionCreate, ActionSkip)
	}
	for _, parent := range parents {
		if err := fn(parent); err != nil {
			return err
		}
	}
	return nil
}

func (c *copier) finish() error {
	c.finishShared()
	c.report.InventoryComplete = true
	if c.conflict || !c.apply {
		for _, item := range c.plans {
			c.report.Results = append(c.report.Results, item.result)
		}
		if c.conflict {
			c.report.Readiness = "blocked by conflicts; nothing written"
			return errors.New("project copy aborted before any write: destination content differs")
		}
		return c.incomplete(false)
	}
	for _, item := range c.plans {
		if item.write == nil {
			c.report.Results = append(c.report.Results, item.result)
			continue
		}
		if err := item.write(); err != nil {
			item.result.Action = ActionBlocked
			item.result.Reason = "create failed"
			c.report.Results = append(c.report.Results, item.result)
			c.report.Readiness = "copy stopped after a create failure; re-run to continue"
			return err
		}
		c.report.Results = append(c.report.Results, item.result)
	}
	return c.incomplete(true)
}

func (c *copier) incomplete(applied bool) error {
	switch {
	case !applied && c.blocked:
		c.report.Readiness = "plan incomplete: blocked resources will be skipped on apply"
		return errors.New("project copy plan incomplete: blocked resources were not included")
	case !applied && c.skipped:
		c.report.Readiness = "plan incomplete: requested git-backed resources will be skipped on apply"
		return errors.New("project copy plan incomplete: requested git-backed resources were not included")
	case !applied && c.pending:
		c.report.Readiness = "plan incomplete: inline secrets need a value"
		return errors.New("project copy plan incomplete: inline secrets need a value")
	case !applied:
		c.report.Readiness = "plan only; pass --apply to copy"
		return nil
	case c.blocked:
		c.report.Readiness = "copied with blocked resources skipped"
		return errors.New("project copy incomplete: blocked resources were not copied")
	case c.skipped:
		c.report.Readiness = "copied with requested resources skipped"
		if c.pending {
			c.report.Readiness = "copied with requested resources skipped; inline secrets need a value"
		}
		return errors.New("project copy incomplete: requested git-backed resources were not copied")
	case c.pending:
		c.report.Readiness = "copied; create inline secrets before running the destination project"
		return errors.New("project copy incomplete: inline secrets need a value")
	default:
		c.report.Readiness = "copied"
		return nil
	}
}

func (c *copier) planProject() error {
	source, err := c.gw.Get("project", c.scope.Source, c.scope.Source, "")
	if err != nil {
		return err
	}
	if !source.Found {
		return errors.New("source project not found")
	}
	dest, err := c.gw.Get("project", c.scope.Destination, c.scope.Destination, "")
	if err != nil {
		return err
	}
	if dest.Found {
		c.keep(Result{Kind: "project", Identifier: c.scope.Destination, Action: ActionSkip, Reason: "destination project already exists; metadata left unchanged"})
		return nil
	}
	fields := map[string]any{
		"identifier":    c.scope.Destination,
		"name":          c.scope.Destination,
		"orgIdentifier": c.scope.Org,
	}
	if description := stringField(source.Raw, "description"); description != "" {
		fields["description"] = description
	}
	if color := stringField(source.Raw, "color"); color != "" {
		fields["color"] = color
	}
	if source.Raw["tags"] != nil {
		fields["tags"] = source.Raw["tags"]
	}
	if source.Raw["modules"] != nil {
		fields["modules"] = source.Raw["modules"]
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	body := string(raw)
	c.queue(Result{Kind: "project", Identifier: c.scope.Destination, Action: ActionCreate, Reason: "created with destination identifier as the project name"}, func() error {
		return c.gw.Create("project", c.scope.Destination, c.scope.Destination, "", body)
	})
	return nil
}

func (c *copier) scanSecretManagers() error {
	items, err := c.list("secret", c.scope.Source, "")
	if err != nil {
		return err
	}
	for _, item := range items {
		full, err := c.gw.Get("secret", c.scope.Source, item.ID, "")
		if err != nil {
			return err
		}
		if !full.Found {
			continue
		}
		sec, err := secretFromMap(full.Raw)
		if err != nil || sec == nil {
			continue
		}
		manager := secretManager(sec)
		if id, ok := projectScopedID(manager); ok {
			c.managers[id] = true
		}
	}
	return nil
}

func (c *copier) planSecrets() error {
	items, err := c.list("secret", c.scope.Source, "")
	if err != nil {
		return err
	}
	for _, item := range items {
		full, err := c.gw.Get("secret", c.scope.Source, item.ID, "")
		if err != nil {
			return err
		}
		if !full.Found {
			c.block("secret", item.ID, "secret could not be read")
			continue
		}
		sec, err := secretFromMap(full.Raw)
		if err != nil || sec == nil || sec.Identifier == "" {
			c.block("secret", item.ID, "unsupported or unproven secret schema")
			continue
		}
		if (sec.Org != "" && sec.Org != c.scope.Org) || (sec.Project != "" && sec.Project != c.scope.Source) {
			c.block("secret", sec.Identifier, "secret is outside the source project")
			continue
		}
		classified := classify(sec)
		switch classified.Capability {
		case ReferenceOnly:
			spec, err := specObject(sec.Spec, false)
			if err != nil {
				c.block("secret", sec.Identifier, "reference secret could not be prepared")
				continue
			}
			c.planReferenceSecret(sec.Identifier, sec.Name, spec)
		case MappingRequired:
			c.planInlineSecret(sec.Identifier, sec.Name, sec.Spec)
		default:
			c.block("secret", sec.Identifier, "unsupported or unproven secret schema")
		}
	}
	return nil
}

func (c *copier) planInlineSecret(id, name string, spec map[string]json.RawMessage) {
	want, err := inlineSecretView(id, name, spec)
	if err != nil {
		c.block("secret", id, "inline secret could not be prepared")
		return
	}
	dest, err := c.gw.Get("secret", c.scope.Destination, id, "")
	if err != nil {
		c.block("secret", id, "destination secret could not be read")
		return
	}
	if dest.Found {
		got, gerr := secretFromMap(dest.Raw)
		if gerr != nil || got == nil || !secretMetaMatches(want, secretAsMap(got)) {
			c.conflictResult("secret", id)
			return
		}
		c.keep(Result{Kind: "secret", Identifier: id, Action: ActionSkip, Reason: "destination secret metadata matches"})
		return
	}
	c.pending = true
	c.report.SecretsPendingValue = append(c.report.SecretsPendingValue, id)
	c.keep(Result{Kind: "secret", Identifier: id, Action: ActionSkipped, Reason: "inline secret needs a value; create it in the destination"})
}

func (c *copier) planReferenceSecret(id, name string, spec map[string]any) {
	want := map[string]any{
		"identifier": id, "name": name, "orgIdentifier": c.scope.Org, "projectIdentifier": c.scope.Destination,
		"type": "SecretText", "spec": spec,
	}
	dest, err := c.gw.Get("secret", c.scope.Destination, id, "")
	if err != nil {
		c.block("secret", id, "destination secret could not be read")
		return
	}
	if dest.Found {
		got, _ := secretFromMap(dest.Raw)
		if got != nil && secretMetaMatches(want, secretAsMap(got)) {
			c.keep(Result{Kind: "secret", Identifier: id, Action: ActionSkip, Reason: "destination secret metadata matches"})
			return
		}
		c.conflictResult("secret", id)
		return
	}
	raw, err := json.Marshal(want)
	if err != nil {
		c.block("secret", id, "reference secret could not be prepared")
		return
	}
	body := string(raw)
	c.queue(Result{Kind: "secret", Identifier: id, Action: ActionCreate, Reason: "external reference copied; value path was not printed"}, func() error {
		return c.gw.Create("secret", c.scope.Destination, id, "", body)
	})
}

func inlineSecretView(id, name string, spec map[string]json.RawMessage) (map[string]any, error) {
	prepared, err := specObject(spec, true)
	if err != nil {
		return nil, err
	}
	delete(prepared, "value")
	return map[string]any{
		"identifier": id, "name": name, "type": "SecretText", "spec": prepared,
	}, nil
}

func secretAsMap(sec *secret) map[string]any {
	raw, err := json.Marshal(sec)
	if err != nil {
		return nil
	}
	var out map[string]any
	if json.Unmarshal(raw, &out) != nil {
		return nil
	}
	return out
}

func secretFromMap(raw map[string]any) (*secret, error) {
	if raw == nil {
		return nil, errors.New("missing secret")
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var sec secret
	if err := json.Unmarshal(data, &sec); err != nil {
		return nil, err
	}
	return &sec, nil
}

func secretManager(sec *secret) string {
	if sec == nil || sec.Spec == nil {
		return ""
	}
	var manager string
	if json.Unmarshal(sec.Spec["secretManagerIdentifier"], &manager) != nil {
		return ""
	}
	return manager
}

func projectScopedID(id string) (string, bool) {
	id = strings.TrimSpace(id)
	if id == "" || strings.Contains(id, ".") {
		return "", false
	}
	return id, ValidIdentifier(id)
}

func (c *copier) planConnectors(managersOnly bool) error {
	items, err := c.list("connector", c.scope.Source, "")
	if err != nil {
		return err
	}
	for _, item := range items {
		id := item.ID
		if id == "" {
			c.block("connector", "", "connector identifier missing")
			continue
		}
		if c.managers[id] != managersOnly {
			continue
		}
		full, err := c.gw.Get("connector", c.scope.Source, id, "")
		if err != nil {
			return err
		}
		raw := item.Raw
		if full.Found && full.Raw != nil {
			raw = full.Raw
		}
		if isRemote(raw) {
			c.skipGit("connector", id, "git-backed connector skipped")
			continue
		}
		if id == "harnessSecretManager" || truthy(raw["harnessManaged"]) {
			c.keep(Result{Kind: "connector", Identifier: id, Action: ActionSkipped, Reason: "harness-managed connector; not copied"})
			continue
		}
		if project := stringField(raw, "projectIdentifier"); project != "" && project != c.scope.Source {
			c.noteShared("connector " + id)
			continue
		}
		source := cleanRoot(raw)
		c.rewriteObj(source)
		c.observe(source)
		dest, err := c.gw.Get("connector", c.scope.Destination, id, "")
		if err != nil {
			return err
		}
		if dest.Found {
			cleaned := cleanRoot(dest.Raw)
			c.rewriteObj(cleaned)
			if canonicalJSON(cleaned) == canonicalJSON(source) {
				c.keep(Result{Kind: "connector", Identifier: id, Action: ActionSkip, Reason: "destination matches"})
				continue
			}
			c.conflictResult("connector", id)
			continue
		}
		body, err := json.Marshal(source)
		if err != nil {
			return err
		}
		payload := string(body)
		c.queue(Result{Kind: "connector", Identifier: id, Action: ActionCreate}, func() error {
			return c.gw.Create("connector", c.scope.Destination, id, "", payload)
		})
	}
	return nil
}

func (c *copier) planYAML(noun, parent string) error {
	items, err := c.list(noun, c.scope.Source, parent)
	if err != nil {
		return err
	}
	for _, item := range items {
		id := item.ID
		reportID := id
		if parent != "" {
			reportID = parent + "/" + id
		}
		if id == "" {
			c.block(noun, "", noun+" identifier missing")
			continue
		}
		doc := item.Doc
		raw := item.Raw
		if doc == "" || isRemote(raw) {
			full, err := c.gw.Get(noun, c.scope.Source, id, parent)
			if err != nil {
				return err
			}
			if full.Found {
				if full.Doc != "" {
					doc = full.Doc
				}
				if full.Raw != nil {
					raw = full.Raw
				}
			}
		}
		if isRemote(raw) {
			c.skipGit(noun, reportID, "git-backed resource skipped")
			continue
		}
		if doc == "" {
			c.block(noun, reportID, "definition missing")
			continue
		}
		rewritten, err := c.rewriteDoc(doc)
		if err != nil {
			c.block(noun, reportID, "definition could not be rewritten")
			continue
		}
		if noun == "trigger" {
			rewritten, err = disableTriggerDoc(rewritten)
			if err != nil {
				c.block(noun, reportID, "definition could not be rewritten")
				continue
			}
		}
		c.observeDoc(rewritten)
		c.notePipelineRefs(rewritten)
		dest, err := c.gw.Get(noun, c.scope.Destination, id, parent)
		if err != nil {
			return err
		}
		if !dest.Found {
			body := rewritten
			kind, resourceID, resourceParent := noun, id, parent
			reason := ""
			if noun == "trigger" {
				reason = "created disabled"
			}
			c.queue(Result{Kind: kind, Identifier: reportID, Action: ActionCreate, Reason: reason}, func() error {
				return c.gw.Create(kind, c.scope.Destination, resourceID, resourceParent, body)
			})
			continue
		}
		destDoc := dest.Doc
		if destDoc == "" {
			c.conflictResult(noun, reportID)
			continue
		}
		destRewritten, rerr := c.rewriteDoc(destDoc)
		same := rerr == nil && sameDocument(rewritten, destRewritten)
		if noun == "trigger" && rerr == nil {
			same = sameTrigger(rewritten, destRewritten)
		}
		if !same {
			c.conflictResult(noun, reportID)
			continue
		}
		c.keep(Result{Kind: noun, Identifier: reportID, Action: ActionSkip, Reason: "destination matches"})
	}
	return nil
}

func (c *copier) planObject(noun, parent string) error {
	items, err := c.list(noun, c.scope.Source, parent)
	if err != nil {
		return err
	}
	for _, item := range items {
		raw := item.Raw
		if objectNeedsGet(noun, raw) {
			full, err := c.gw.Get(noun, c.scope.Source, item.ID, parent)
			if err != nil {
				return err
			}
			if full.Found && full.Raw != nil {
				raw = full.Raw
			}
		}
		if isRemote(raw) {
			c.skipGit(noun, reportID(parent, item.ID), "git-backed resource skipped")
			continue
		}
		if project := stringField(raw, "projectIdentifier", "project_id"); project != "" && project != c.scope.Source {
			c.noteShared(noun + " " + stringField(raw, "identifier"))
			continue
		}
		obj := objectBody(noun, raw)
		id := stringField(obj, "identifier")
		if id == "" {
			id = item.ID
		}
		if id == "" {
			c.block(noun, "", noun+" identifier missing")
			continue
		}
		c.rewriteObj(obj)
		c.observe(obj)
		report := reportID(parent, id)
		dest, err := c.gw.Get(noun, c.scope.Destination, id, parent)
		if err != nil {
			return err
		}
		if dest.Found {
			got := objectBody(noun, dest.Raw)
			c.rewriteObj(got)
			if canonicalJSON(got) == canonicalJSON(obj) {
				c.keep(Result{Kind: noun, Identifier: report, Action: ActionSkip, Reason: "destination matches"})
				continue
			}
			c.conflictResult(noun, report)
			continue
		}
		body, err := json.Marshal(obj)
		if err != nil {
			return err
		}
		payload := string(body)
		kind, resourceID, resourceParent := noun, id, parent
		c.queue(Result{Kind: kind, Identifier: report, Action: ActionCreate}, func() error {
			return c.gw.Create(kind, c.scope.Destination, resourceID, resourceParent, payload)
		})
	}
	return nil
}

func objectNeedsGet(noun string, raw map[string]any) bool {
	switch noun {
	case "policy":
		return stringField(raw, "rego") == ""
	case "policy_set":
		return raw["policies"] == nil
	default:
		return raw == nil
	}
}

func objectBody(noun string, raw map[string]any) map[string]any {
	if raw == nil {
		raw = map[string]any{}
	}
	id := stringField(raw, "identifier")
	switch noun {
	case "policy":
		return map[string]any{"identifier": id, "name": firstNonEmpty(stringField(raw, "name"), id), "rego": stringField(raw, "rego")}
	case "policy_set":
		return map[string]any{
			"identifier": id, "name": firstNonEmpty(stringField(raw, "name"), id),
			"type": stringField(raw, "type"), "action": stringField(raw, "action"),
			"enabled": raw["enabled"], "policies": raw["policies"],
		}
	default:
		return cleanRoot(raw)
	}
}

func reportID(parent, id string) string {
	if parent == "" {
		return id
	}
	return parent + "/" + id
}

type templateUnit struct {
	id, version, reportID, doc string
	stable                     bool
}

func (c *copier) planTemplates() error {
	tops, err := c.list("template", c.scope.Source, "")
	if err != nil {
		return err
	}
	var units []templateUnit
	seenTop := map[string]bool{}
	for _, top := range tops {
		if top.ID == "" || seenTop[top.ID] {
			continue
		}
		seenTop[top.ID] = true
		versions, err := c.list("template_version", c.scope.Source, top.ID)
		if err != nil {
			return err
		}
		if len(versions) == 0 {
			versions = []listed{top}
		}
		for _, version := range versions {
			ver := version.Version
			if ver == "" {
				ver = version.ID
			}
			if ver == "" || (ver == top.ID && version.Version == "" && top.Version == "") {
				full, gerr := c.gw.Get("template", c.scope.Source, top.ID, "")
				if gerr != nil {
					return gerr
				}
				ver = full.Version
				if ver == "" {
					ver = versionLabel(full.Doc)
				}
				version.Doc = firstNonEmpty(version.Doc, full.Doc)
				version.Raw = full.Raw
				version.Stable = version.Stable || full.Stable
			}
			report := top.ID + "/" + ver
			if isRemote(version.Raw) || isRemote(top.Raw) {
				c.skipGit("template", report, "git-backed template skipped")
				continue
			}
			doc := version.Doc
			stable := version.Stable || top.Stable && (version.Version == "" || version.Version == top.Version)
			if doc == "" || ver == "" {
				full, gerr := c.gw.Get("template_version", c.scope.Source, ver, top.ID)
				if gerr != nil {
					return gerr
				}
				if !full.Found && doc == "" {
					c.block("template", report, "template version not found")
					continue
				}
				doc = firstNonEmpty(doc, full.Doc)
				stable = stable || full.Stable
				if ver == "" {
					ver = firstNonEmpty(full.Version, versionLabel(doc))
					report = top.ID + "/" + ver
				}
			}
			rewritten, rerr := c.rewriteDoc(doc)
			if rerr != nil || rewritten == "" {
				c.block("template", report, "template definition missing")
				continue
			}
			units = append(units, templateUnit{id: top.ID, version: ver, reportID: report, doc: rewritten, stable: stable})
		}
	}
	stableAt := map[string]string{}
	for _, unit := range orderTemplateUnits(units) {
		c.observeDoc(unit.doc)
		c.notePipelineRefs(unit.doc)
		if _, known := stableAt[unit.id]; !known {
			got, lerr := c.destStable(unit.id)
			if lerr != nil {
				return lerr
			}
			stableAt[unit.id] = got
		}
		dest, err := c.gw.Get("template_version", c.scope.Destination, unit.version, unit.id)
		if err != nil {
			return err
		}
		if !dest.Found {
			unitCopy := unit
			c.queue(Result{Kind: "template", Identifier: unit.reportID, Action: ActionCreate}, func() error {
				if err := c.gw.Create("template", c.scope.Destination, unitCopy.id, "", unitCopy.doc); err != nil {
					return err
				}
				if !unitCopy.stable {
					return nil
				}
				return c.gw.SetStable(c.scope.Destination, unitCopy.id, unitCopy.version)
			})
			continue
		}
		destDoc := dest.Doc
		destRewritten, rerr := c.rewriteDoc(destDoc)
		if rerr != nil || !sameDocument(unit.doc, destRewritten) {
			c.conflictResult("template", unit.reportID)
			continue
		}
		destStable := stableAt[unit.id]
		if dest.Stable {
			destStable = unit.version
		}
		if unit.stable && destStable != unit.version {
			unitCopy := unit
			c.queue(Result{Kind: "template", Identifier: unit.reportID, Action: ActionCreate, Reason: "set stable version"}, func() error {
				return c.gw.SetStable(c.scope.Destination, unitCopy.id, unitCopy.version)
			})
			continue
		}
		c.keep(Result{Kind: "template", Identifier: unit.reportID, Action: ActionSkip, Reason: "destination matches"})
	}
	return nil
}

func (c *copier) destStable(id string) (string, error) {
	versions, err := c.gw.List("template_version", c.scope.Destination, id)
	if err != nil {
		return "", err
	}
	for _, version := range versions {
		if version.Stable {
			if version.Version != "" {
				return version.Version, nil
			}
			return version.ID, nil
		}
	}
	return "", nil
}

func versionLabel(doc string) string {
	var node any
	if yaml.Unmarshal([]byte(doc), &node) != nil {
		return ""
	}
	plain, _ := yamlToPlain(node).(map[string]any)
	template, _ := plain["template"].(map[string]any)
	if template == nil {
		template = plain
	}
	return stringField(template, "versionLabel", "version")
}

func orderTemplateUnits(units []templateUnit) []templateUnit {
	if len(units) < 2 {
		return units
	}
	ids := map[string]struct{}{}
	byID := map[string][]templateUnit{}
	for _, unit := range units {
		ids[unit.id] = struct{}{}
		byID[unit.id] = append(byID[unit.id], unit)
	}
	for id := range byID {
		sort.Slice(byID[id], func(i, j int) bool {
			return versionLess(byID[id][i].version, byID[id][j].version)
		})
	}
	prereq := map[string]map[string]struct{}{}
	for _, unit := range units {
		for _, ref := range templateRefs(unit.doc) {
			dep, ok := projectTemplateID(ref)
			if !ok || dep == unit.id {
				continue
			}
			if _, known := ids[dep]; !known {
				continue
			}
			if prereq[unit.id] == nil {
				prereq[unit.id] = map[string]struct{}{}
			}
			prereq[unit.id][dep] = struct{}{}
		}
	}
	remaining := map[string]struct{}{}
	for id := range ids {
		remaining[id] = struct{}{}
	}
	var orderedIDs []string
	for len(remaining) > 0 {
		var ready []string
		for id := range remaining {
			blocked := false
			for dep := range prereq[id] {
				if _, still := remaining[dep]; still {
					blocked = true
					break
				}
			}
			if !blocked {
				ready = append(ready, id)
			}
		}
		if len(ready) == 0 {
			for id := range remaining {
				ready = append(ready, id)
			}
			sort.Strings(ready)
			orderedIDs = append(orderedIDs, ready...)
			break
		}
		sort.Strings(ready)
		for _, id := range ready {
			orderedIDs = append(orderedIDs, id)
			delete(remaining, id)
		}
	}
	out := make([]templateUnit, 0, len(units))
	for _, id := range orderedIDs {
		out = append(out, byID[id]...)
	}
	return out
}

func templateRefs(doc string) []string {
	var node any
	if yaml.Unmarshal([]byte(doc), &node) != nil {
		return nil
	}
	var refs []string
	walk(yamlToPlain(node), func(key string, value any) {
		s, ok := value.(string)
		if key != "templateRef" || !ok || s == "" {
			return
		}
		refs = append(refs, s)
	})
	return refs
}

func projectTemplateID(ref string) (string, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" || strings.Contains(ref, ".") {
		return "", false
	}
	return ref, true
}

func (c *copier) planDelegates() error {
	if len(c.selectors) == 0 {
		return nil
	}
	items, err := c.gw.Delegates()
	if err != nil {
		return err
	}
	names := make([]string, 0, len(c.selectors))
	for name := range c.selectors {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		matched := ""
		for _, item := range items {
			if !delegateConnected(item) || !delegateMatches(item, name) {
				continue
			}
			matched = firstNonEmpty(stringField(item, "groupName"), stringField(item, "delegateGroupIdentifier"))
			break
		}
		result := Result{Kind: "delegate", Identifier: name, Action: ActionShared, Reason: "no connected delegate matches this selector"}
		if matched != "" {
			result.Reason = "matched connected delegate " + matched + "; delegates are not copied"
		}
		c.report.DelegateSelectors = append(c.report.DelegateSelectors, result)
	}
	return nil
}

func delegateConnected(item map[string]any) bool {
	status := strings.ToUpper(strings.TrimSpace(stringField(item, "connectivityStatus", "status")))
	switch status {
	case "DISCONNECTED", "DOWN", "EXPIRED", "DEAD":
		return false
	case "CONNECTED", "ACTIVE", "SUCCESS":
		return true
	}
	if b, ok := item["activelyConnected"].(bool); ok {
		return b
	}
	return strings.EqualFold(strings.TrimSpace(stringField(item, "activelyConnected")), "true")
}

func delegateMatches(item map[string]any, selector string) bool {
	if stringField(item, "groupName", "name", "delegateGroupIdentifier") == selector {
		return true
	}
	switch tags := item["tags"].(type) {
	case []any:
		for _, tag := range tags {
			switch value := tag.(type) {
			case string:
				if value == selector {
					return true
				}
			case map[string]any:
				if stringField(value, "key", "name", "value") == selector {
					return true
				}
			}
		}
	}
	return false
}

func (c *copier) list(noun, project, parent string) ([]listed, error) {
	items, err := c.gw.List(noun, project, parent)
	if err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].ID == items[j].ID {
			return items[i].Version < items[j].Version
		}
		return items[i].ID < items[j].ID
	})
	return items, nil
}

func (c *copier) idsOf(kind string, actions ...Action) []string {
	want := map[Action]bool{}
	for _, action := range actions {
		want[action] = true
	}
	var ids []string
	seen := map[string]bool{}
	consider := func(result Result) {
		if result.Kind != kind || !want[result.Action] || seen[result.Identifier] {
			return
		}
		seen[result.Identifier] = true
		ids = append(ids, result.Identifier)
	}
	for _, item := range c.report.Results {
		consider(item)
	}
	for _, item := range c.plans {
		consider(item.result)
	}
	return ids
}

func (c *copier) queue(result Result, write func() error) {
	c.plans = append(c.plans, work{result: result, write: write})
}

func (c *copier) keep(result Result) {
	c.report.Results = append(c.report.Results, result)
}

func (c *copier) skipGit(kind, id, reason string) {
	c.skipped = true
	c.keep(Result{Kind: kind, Identifier: id, Action: ActionSkipped, Reason: reason})
}

func (c *copier) block(kind, id, reason string) {
	c.blocked = true
	c.keep(Result{Kind: kind, Identifier: id, Action: ActionBlocked, Reason: reason})
}

func (c *copier) conflictResult(kind, id string) {
	c.conflict = true
	c.keep(Result{Kind: kind, Identifier: id, Action: ActionConflict, Reason: "destination content differs"})
}

func (c *copier) noteShared(id string) {
	c.shared[id] = struct{}{}
}

func (c *copier) finishShared() {
	ids := make([]string, 0, len(c.shared))
	for id := range c.shared {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		c.report.Results = append(c.report.Results, Result{Kind: "reference", Identifier: id, Action: ActionShared, Reason: "org or account reference; not copied"})
	}
}

func (c *copier) observe(v any) {
	walk(v, func(key string, value any) {
		if key == "delegateSelectors" {
			if items, ok := asSlice(value); ok {
				for _, item := range items {
					if s, ok := item.(string); ok && s != "" {
						c.selectors[s] = struct{}{}
					}
				}
			}
		}
		if s, ok := value.(string); ok && scopedRef.MatchString(s) {
			c.shared[s] = struct{}{}
		}
	})
}

func (c *copier) observeDoc(doc string) {
	var node any
	if yaml.Unmarshal([]byte(doc), &node) != nil {
		return
	}
	c.observe(yamlToPlain(node))
}

func (c *copier) notePipelineRefs(doc string) {
	var node any
	if yaml.Unmarshal([]byte(doc), &node) != nil {
		return
	}
	c.notePipelineNode(yamlToPlain(node))
}

func (c *copier) notePipelineNode(v any) {
	switch n := v.(type) {
	case map[string]any:
		if stringField(n, "type") == "Pipeline" {
			if spec, ok := n["spec"].(map[string]any); ok {
				project := stringField(spec, "project")
				if project != "" && project != c.scope.Destination && !strings.HasPrefix(project, "account.") && !strings.HasPrefix(project, "org.") {
					c.noteShared("project." + project)
				}
			}
		}
		for _, child := range n {
			c.notePipelineNode(child)
		}
	case []any:
		for _, child := range n {
			c.notePipelineNode(child)
		}
	}
}

func (c *copier) rewriteDoc(doc string) (string, error) {
	return rewriteYAML(doc, c.rewriteOpts())
}

func (c *copier) rewriteObj(v any) {
	rewriteTree(v, c.rewriteOpts())
}

func (c *copier) rewriteOpts() rewriteOpts {
	return rewriteOpts{Org: c.scope.Org, Project: c.scope.Destination, SourceOrg: c.scope.Org, SourceProject: c.scope.Source}
}

func specObject(spec map[string]json.RawMessage, inline bool) (map[string]any, error) {
	var manager, kind string
	if json.Unmarshal(spec["secretManagerIdentifier"], &manager) != nil || manager == "" {
		return nil, errors.New("missing secret manager")
	}
	if json.Unmarshal(spec["valueType"], &kind) != nil || kind == "" {
		return nil, errors.New("missing value type")
	}
	if inline {
		return map[string]any{"type": "SecretText", "valueType": "Inline", "secretManagerIdentifier": manager}, nil
	}
	var value string
	if json.Unmarshal(spec["value"], &value) != nil || value == "" {
		return nil, errors.New("missing reference path")
	}
	return map[string]any{"type": "SecretText", "valueType": "Reference", "secretManagerIdentifier": manager, "value": value}, nil
}

func secretMetaMatches(want, got map[string]any) bool {
	if got == nil || stringField(got, "name") != stringField(want, "name") || stringField(got, "type") != "SecretText" {
		return false
	}
	wantSpec, _ := want["spec"].(map[string]any)
	gotSpec, _ := got["spec"].(map[string]any)
	if gotSpec == nil || stringField(gotSpec, "valueType") != stringField(wantSpec, "valueType") {
		return false
	}
	if stringField(gotSpec, "secretManagerIdentifier") != stringField(wantSpec, "secretManagerIdentifier") {
		return false
	}
	if strings.EqualFold(stringField(wantSpec, "valueType"), "Reference") {
		return stringField(gotSpec, "value") == stringField(wantSpec, "value")
	}
	return true
}

func versionLess(a, b string) bool {
	ai, aok := versionNum(a)
	bi, bok := versionNum(b)
	if aok && bok && ai != bi {
		return ai < bi
	}
	return a < b
}

func versionNum(s string) (int, bool) {
	s = strings.TrimPrefix(strings.TrimPrefix(s, "v"), "V")
	n, err := strconv.Atoi(s)
	return n, err == nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
