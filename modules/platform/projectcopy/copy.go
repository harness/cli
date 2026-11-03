// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package projectcopy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Copy plans a same-org CD-core copy from scope.Source to scope.Destination.
// apply creates the destination project when it is missing and writes resources.
// Conflicting destination content aborts before any write.
func Copy(ctx context.Context, t *Transport, scope Scope, apply bool) (Report, error) {
	c := &copier{ctx: ctx, transport: t, scope: scope, apply: apply, report: NewCopyReport(scope, apply), selectors: map[string]struct{}{}, shared: map[string]struct{}{}}
	err := c.run()
	return c.report, err
}

func (c *copier) rewriteOpts(disableTriggers bool) rewriteOpts {
	return rewriteOpts{Org: c.scope.Org, Project: c.scope.Destination, SourceOrg: c.scope.Org, SourceProject: c.scope.Source, DisableTriggers: disableTriggers}
}

func (c *copier) rewriteDoc(doc string, disableTriggers bool) (string, error) {
	return rewriteYAML(doc, c.rewriteOpts(disableTriggers))
}

func (c *copier) rewriteObj(v any) {
	rewriteTree(v, c.rewriteOpts(false))
}

type copier struct {
	ctx         context.Context
	transport   *Transport
	scope       Scope
	apply       bool
	report      Report
	selectors   map[string]struct{}
	shared      map[string]struct{}
	plans       []work
	conflict    bool
	blocked     bool
	skipped     bool
	envIDs      []string
	pipelineIDs []string
}

type work struct {
	result Result
	write  func() error
}

func (c *copier) run() error {
	if err := c.scope.Validate(); err != nil {
		return err
	}
	if err := c.planProject(); err != nil {
		return err
	}
	if err := c.planSecrets(); err != nil {
		return err
	}
	if err := c.planConnectors(); err != nil {
		return err
	}
	if err := c.planYAMLList("service", "/ng/api/servicesV2", "service", "", nil); err != nil {
		return err
	}
	if err := c.planYAMLList("environment", "/ng/api/environmentsV2", "environment", "", nil); err != nil {
		return err
	}
	c.envIDs = c.idsOf("environment", ActionCreate, ActionSkip)
	if err := c.planChildren("infrastructure", c.envIDs, c.planInfrastructures); err != nil {
		return err
	}
	if err := c.planChildren("service_override", c.envIDs, c.planOverrides); err != nil {
		return err
	}
	if err := c.planTemplates(); err != nil {
		return err
	}
	if err := c.planPipelines(); err != nil {
		return err
	}
	c.pipelineIDs = c.idsOf("pipeline", ActionCreate, ActionSkip)
	if err := c.planChildren("input_set", c.pipelineIDs, c.planInputSets); err != nil {
		return err
	}
	if err := c.planChildren("trigger", c.pipelineIDs, c.planTriggers); err != nil {
		return err
	}
	if err := c.planPolicies(); err != nil {
		return err
	}
	if err := c.planPolicySets(); err != nil {
		return err
	}
	if err := c.planDelegates(); err != nil {
		return err
	}
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
		if c.blocked {
			c.report.Readiness = "plan incomplete: blocked resources will be skipped on apply"
			return errors.New("project copy plan incomplete: blocked resources were not included")
		}
		if c.skipped {
			c.report.Readiness = "plan incomplete: requested git-backed resources will be skipped on apply"
			return errors.New("project copy plan incomplete: requested git-backed resources were not included")
		}
		c.report.Readiness = "plan only; pass --apply to copy"
		return nil
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
	if c.blocked {
		c.report.Readiness = "copied with blocked resources skipped"
		return errors.New("project copy incomplete: blocked resources were not copied")
	}
	if c.skipped {
		c.report.Readiness = "copied with requested resources skipped"
		if len(c.report.SecretsPendingValue) > 0 {
			c.report.Readiness = "copied with requested resources skipped; update inline secrets before running the destination project"
		}
		return errors.New("project copy incomplete: requested git-backed resources were not copied")
	}
	c.report.Readiness = "copied"
	if len(c.report.SecretsPendingValue) > 0 {
		c.report.Readiness = "copied; update inline secrets before running the destination project"
	}
	return nil
}

func (c *copier) planProject() error {
	source, found, err := c.getObject("/ng/api/projects/"+url.PathEscape(c.scope.Source), c.query(""))
	if err != nil {
		return err
	}
	if !found {
		return errors.New("source project not found")
	}
	project, _ := digMap(source, "data", "project")
	if project == nil {
		project = source
	}
	_, destFound, err := c.getObject("/ng/api/projects/"+url.PathEscape(c.scope.Destination), c.query(""))
	if err != nil {
		return err
	}
	if destFound {
		c.keep(Result{Kind: "project", Identifier: c.scope.Destination, Action: ActionSkip, Reason: "destination project already exists; metadata left unchanged"})
		return nil
	}
	fields := map[string]any{
		"identifier":    c.scope.Destination,
		"name":          c.scope.Destination,
		"orgIdentifier": c.scope.Org,
	}
	if description := stringField(project, "description"); description != "" {
		fields["description"] = description
	}
	if color := stringField(project, "color"); color != "" {
		fields["color"] = color
	}
	if project["tags"] != nil {
		fields["tags"] = project["tags"]
	}
	if project["modules"] != nil {
		fields["modules"] = project["modules"]
	}
	body := map[string]any{"project": fields}
	raw, _ := json.Marshal(body)
	c.queue(Result{Kind: "project", Identifier: c.scope.Destination, Action: ActionCreate, Reason: "created with destination identifier as the project name"}, func() error {
		return c.postJSON("/ng/api/projects", c.query(""), string(raw))
	})
	return nil
}

func (c *copier) planSecrets() error {
	items, err := c.list(http.MethodPost, secretsPath+"/list/secrets", c.query(c.scope.Source), `{"filterType":"Secret"}`, "pageIndex", "pageSize", "secret")
	if err != nil {
		return err
	}
	sort.Slice(items, func(i, j int) bool { return stringField(items[i], "identifier") < stringField(items[j], "identifier") })
	for _, item := range items {
		raw, _ := json.Marshal(item)
		var secret secret
		if json.Unmarshal(raw, &secret) != nil || secret.Identifier == "" {
			c.block("secret", stringField(item, "identifier"), "unsupported or unproven secret schema")
			continue
		}
		if (secret.Org != "" && secret.Org != c.scope.Org) || (secret.Project != "" && secret.Project != c.scope.Source) {
			c.block("secret", secret.Identifier, "secret is outside the source project")
			continue
		}
		id := secret.Identifier
		classified := classify(&secret)
		switch classified.Capability {
		case ReferenceOnly:
			spec, err := specObject(secret.Spec, false)
			if err != nil {
				c.block("secret", id, "reference secret could not be prepared")
				continue
			}
			c.planSecret(id, secret.Name, spec, "external reference copied; value path was not printed", false)
		case MappingRequired:
			if secret.Type != "SecretText" {
				c.block("secret", id, "unsupported secret type")
				continue
			}
			spec, err := specObject(secret.Spec, true)
			if err != nil {
				c.block("secret", id, "inline secret could not be prepared")
				continue
			}
			c.planSecret(id, secret.Name, spec, "placeholder value "+InlineSecretPlaceholder+"; update before use", true)
		default:
			c.block("secret", id, "unsupported or unproven secret schema")
		}
	}
	return nil
}

func (c *copier) planSecret(id, name string, spec map[string]any, reason string, pending bool) {
	dest, found, err := c.getObject("/ng/api/v2/secrets/"+url.PathEscape(id), c.query(c.scope.Destination))
	if err != nil {
		c.block("secret", id, "destination secret could not be read")
		return
	}
	payload := map[string]any{"secret": map[string]any{
		"identifier": id, "name": name, "orgIdentifier": c.scope.Org, "projectIdentifier": c.scope.Destination,
		"type": "SecretText", "spec": spec,
	}}
	if found {
		got, _ := digMap(dest, "data", "secret")
		if secretMetaMatches(payload["secret"].(map[string]any), got) {
			c.keep(Result{Kind: "secret", Identifier: id, Action: ActionSkip, Reason: "destination secret metadata matches"})
			return
		}
		c.conflictResult("secret", id)
		return
	}
	raw, _ := json.Marshal(payload)
	if pending {
		c.report.SecretsPendingValue = append(c.report.SecretsPendingValue, id)
	}
	c.queue(Result{Kind: "secret", Identifier: id, Action: ActionCreate, Reason: reason}, func() error {
		return c.postJSON("/ng/api/v2/secrets", c.query(c.scope.Destination), string(raw))
	})
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

func specObject(spec map[string]json.RawMessage, inline bool) (map[string]any, error) {
	var manager, kind string
	if json.Unmarshal(spec["secretManagerIdentifier"], &manager) != nil || manager == "" {
		return nil, errors.New("missing secret manager")
	}
	if json.Unmarshal(spec["valueType"], &kind) != nil || kind == "" {
		return nil, errors.New("missing value type")
	}
	if inline {
		return map[string]any{"type": "SecretText", "valueType": "Inline", "secretManagerIdentifier": manager, "value": InlineSecretPlaceholder}, nil
	}
	var value string
	if json.Unmarshal(spec["value"], &value) != nil || value == "" {
		return nil, errors.New("missing reference path")
	}
	return map[string]any{"type": "SecretText", "valueType": "Reference", "secretManagerIdentifier": manager, "value": value}, nil
}

func (c *copier) planConnectors() error {
	items, err := c.list(http.MethodPost, "/ng/api/connectors/listV2", c.query(c.scope.Source), `{"filterType":"Connector"}`, "pageIndex", "pageSize", "connector")
	if err != nil {
		return err
	}
	for _, item := range items {
		id := stringField(item, "identifier")
		if id == "" {
			c.block("connector", "", "connector identifier missing")
			continue
		}
		if isRemote(item) {
			c.skipGit("connector", id, "git-backed connector skipped")
			continue
		}
		if id == "harnessSecretManager" || truthy(item["harnessManaged"]) {
			c.keep(Result{Kind: "connector", Identifier: id, Action: ActionSkipped, Reason: "harness-managed connector; not copied"})
			continue
		}
		if project := stringField(item, "projectIdentifier"); project != "" && project != c.scope.Source {
			c.noteShared("connector " + id)
			continue
		}
		source := cleanRoot(item)
		c.observe(source)
		c.rewriteObj(source)
		destRaw, found, err := c.getObject("/ng/api/connectors/"+url.PathEscape(id), c.query(c.scope.Destination))
		if err != nil {
			return err
		}
		if found {
			dest, _ := digMap(destRaw, "data", "connector")
			cleaned := cleanRoot(dest)
			c.rewriteObj(cleaned)
			if canonicalJSON(cleaned) == canonicalJSON(source) {
				c.keep(Result{Kind: "connector", Identifier: id, Action: ActionSkip, Reason: "destination matches"})
				continue
			}
			c.conflictResult("connector", id)
			continue
		}
		body, _ := json.Marshal(map[string]any{"connector": source})
		payload := string(body)
		c.queue(Result{Kind: "connector", Identifier: id, Action: ActionCreate}, func() error {
			return c.postJSON("/ng/api/connectors", c.query(c.scope.Destination), payload)
		})
	}
	return nil
}

func (c *copier) planYAMLList(kind, path, root, parent string, extra url.Values) error {
	query := c.query(c.scope.Source)
	for key, values := range extra {
		for _, value := range values {
			query.Set(key, value)
		}
	}
	items, err := c.list(http.MethodGet, path, query, "", "page", "size", root)
	if err != nil {
		return err
	}
	for _, item := range items {
		id := stringField(item, "identifier")
		if id == "" {
			c.block(kind, "", kind+" identifier missing")
			continue
		}
		if isRemote(item) {
			c.skipGit(kind, id, "git-backed resource skipped")
			continue
		}
		sourceDoc, err := c.yamlFor(kind, id, item, root, extra, c.scope.Source)
		if err != nil {
			return err
		}
		if sourceDoc == "" {
			c.block(kind, id, "definition missing")
			continue
		}
		rewritten, err := c.rewriteDoc(sourceDoc, false)
		if err != nil {
			c.block(kind, id, "definition could not be rewritten")
			continue
		}
		c.observeDoc(rewritten)
		destDoc, found, err := c.destYAML(kind, id, root, extra)
		if err != nil {
			return err
		}
		reportID := id
		if parent != "" {
			reportID = parent + "/" + id
		}
		if !found {
			doc := rewritten
			createPath, createQuery := yamlCreate(kind, id, parent)
			c.queue(Result{Kind: kind, Identifier: reportID, Action: ActionCreate}, func() error {
				return c.postCDYAML(kind, createPath, createQuery, doc)
			})
			continue
		}
		destRewritten, err := c.rewriteDoc(destDoc, false)
		if err != nil || !sameDocument(rewritten, destRewritten) {
			c.conflictResult(kind, reportID)
			continue
		}
		c.keep(Result{Kind: kind, Identifier: reportID, Action: ActionSkip, Reason: "destination matches"})
	}
	return nil
}

func yamlCreate(kind, id, parent string) (string, url.Values) {
	switch kind {
	case "service":
		return "/ng/api/servicesV2", nil
	case "environment":
		return "/ng/api/environmentsV2", nil
	case "infrastructure":
		return "/ng/api/infrastructures", nil
	default:
		return "", nil
	}
}

func (c *copier) yamlFor(kind, id string, listed map[string]any, root string, extra url.Values, project string) (string, error) {
	if doc := stringField(listed, "yaml", "yamlPipeline", "inputSetYaml"); doc != "" {
		return doc, nil
	}
	path := yamlGetPath(kind, id)
	query := c.query(project)
	for key, values := range extra {
		for _, value := range values {
			query.Set(key, value)
		}
	}
	obj, found, err := c.getObject(path, query)
	if err != nil || !found {
		if err != nil {
			return "", err
		}
		return entityDocument(listed, root)
	}
	entity, _ := digMap(obj, "data", root)
	if entity == nil {
		entity, _ = digMap(obj, "data")
	}
	if entity == nil {
		entity = obj
	}
	if doc := stringField(entity, "yaml", "yamlPipeline", "inputSetYaml"); doc != "" {
		return doc, nil
	}
	return entityDocument(entity, root)
}

func (c *copier) destYAML(kind, id, root string, extra url.Values) (string, bool, error) {
	obj, found, err := c.getObject(yamlGetPath(kind, id), mergeQuery(c.query(c.scope.Destination), extra))
	if err != nil || !found {
		return "", false, err
	}
	entity, _ := digMap(obj, "data", root)
	if entity == nil {
		entity, _ = digMap(obj, "data")
	}
	if doc := stringField(entity, "yaml", "yamlPipeline", "inputSetYaml"); doc != "" {
		return doc, true, nil
	}
	doc, err := entityDocument(entity, root)
	return doc, true, err
}

func yamlGetPath(kind, id string) string {
	switch kind {
	case "service":
		return "/ng/api/servicesV2/" + url.PathEscape(id)
	case "environment":
		return "/ng/api/environmentsV2/" + url.PathEscape(id)
	case "infrastructure":
		return "/ng/api/infrastructures/" + url.PathEscape(id)
	default:
		return ""
	}
}

func entityDocument(entity map[string]any, root string) (string, error) {
	if entity == nil {
		return "", nil
	}
	cleaned := cleanRoot(entity)
	out, err := yamlMarshal(map[string]any{root: cleaned})
	return out, err
}

func yamlMarshal(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	var plain any
	if err := json.Unmarshal(data, &plain); err != nil {
		return "", err
	}
	out, err := yaml.Marshal(plain)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func (c *copier) planInfrastructures(env string) error {
	return c.planYAMLList("infrastructure", "/ng/api/infrastructures", "infrastructure", env, url.Values{"environmentIdentifier": {env}})
}

func (c *copier) planOverrides(env string) error {
	items, err := c.list(http.MethodGet, "/ng/api/environmentsV2/serviceOverrides", mergeQuery(c.query(c.scope.Source), url.Values{"environmentIdentifier": {env}}), "", "page", "size", "")
	if err != nil {
		return err
	}
	for _, item := range items {
		id := stringField(item, "identifier")
		if id == "" {
			id = env + "/" + stringField(item, "serviceRef", "serviceIdentifier")
		}
		if isRemote(item) {
			c.skipGit("service_override", id, "git-backed resource skipped")
			continue
		}
		source := cleanRoot(item)
		c.observe(source)
		c.rewriteObj(source)
		destRaw, found, err := c.getObject("/ng/api/serviceOverrides/"+url.PathEscape(stringField(item, "identifier")), c.query(c.scope.Destination))
		if err != nil {
			return err
		}
		if found {
			dest, _ := digMap(destRaw, "data")
			cleaned := cleanRoot(dest)
			c.rewriteObj(cleaned)
			if canonicalJSON(cleaned) == canonicalJSON(source) {
				c.keep(Result{Kind: "service_override", Identifier: id, Action: ActionSkip, Reason: "destination matches"})
				continue
			}
			c.conflictResult("service_override", id)
			continue
		}
		body, _ := json.Marshal(source)
		payload := string(body)
		c.queue(Result{Kind: "service_override", Identifier: id, Action: ActionCreate}, func() error {
			return c.postJSON("/ng/api/serviceOverrides", c.query(c.scope.Destination), payload)
		})
	}
	return nil
}

type templateUnit struct {
	id, version, reportID, doc string
	stable                     bool
}

func (c *copier) planTemplates() error {
	items, err := c.list(http.MethodPost, "/template/api/templates/list", mergeQuery(c.query(c.scope.Source), url.Values{"templateListType": {"ALL"}}), `{"filterType":"Template"}`, "page", "size", "")
	if err != nil {
		return err
	}
	var units []templateUnit
	for _, item := range items {
		id := stringField(item, "identifier")
		version := stringField(item, "versionLabel", "version")
		reportID := id + "/" + version
		if isRemote(item) {
			c.skipGit("template", reportID, "git-backed template skipped")
			continue
		}
		query := mergeQuery(c.query(c.scope.Source), url.Values{"versionLabel": {version}})
		obj, found, err := c.getObject("/template/api/templates/"+url.PathEscape(id), query)
		if err != nil {
			return err
		}
		if !found {
			c.block("template", reportID, "template version not found")
			continue
		}
		data, _ := digMap(obj, "data")
		doc := stringField(data, "yaml")
		if doc == "" {
			doc = stringField(obj, "yaml")
		}
		rewritten, err := c.rewriteDoc(doc, false)
		if err != nil || rewritten == "" {
			c.block("template", reportID, "template definition missing")
			continue
		}
		units = append(units, templateUnit{
			id: id, version: version, reportID: reportID, doc: rewritten,
			stable: truthy(item["stableTemplate"]) || truthy(item["stable"]),
		})
	}
	for _, unit := range orderTemplateUnits(units) {
		c.observeDoc(unit.doc)
		destObj, destFound, err := c.getObject("/template/api/templates/"+url.PathEscape(unit.id), mergeQuery(c.query(c.scope.Destination), url.Values{"versionLabel": {unit.version}}))
		if err != nil {
			return err
		}
		if destFound {
			destData, _ := digMap(destObj, "data")
			destDoc := stringField(destData, "yaml")
			destRewritten, rerr := c.rewriteDoc(destDoc, false)
			if rerr != nil || !sameDocument(unit.doc, destRewritten) {
				c.conflictResult("template", unit.reportID)
				continue
			}
			c.keep(Result{Kind: "template", Identifier: unit.reportID, Action: ActionSkip, Reason: "destination matches"})
			continue
		}
		docCopy := unit.doc
		id, version, stable := unit.id, unit.version, unit.stable
		c.queue(Result{Kind: "template", Identifier: unit.reportID, Action: ActionCreate}, func() error {
			if err := c.postYAML("/template/api/templates", c.query(c.scope.Destination), docCopy); err != nil {
				return err
			}
			if !stable {
				return nil
			}
			stablePath := "/template/api/templates/updateStableTemplate/" + url.PathEscape(id) + "/" + url.PathEscape(version)
			return c.postJSON(stablePath, c.query(c.scope.Destination), "")
		})
	}
	return nil
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

func (c *copier) planPipelines() error {
	items, err := c.list(http.MethodPost, "/pipeline/api/pipelines/list", c.query(c.scope.Source), `{"filterType":"PipelineSetup"}`, "page", "size", "")
	if err != nil {
		return err
	}
	for _, item := range items {
		id := stringField(item, "identifier")
		if isRemote(item) {
			c.skipGit("pipeline", id, "git-backed pipeline skipped")
			continue
		}
		doc, found, err := c.pipelineYAML(id, c.scope.Source)
		if err != nil {
			return err
		}
		if !found || doc == "" {
			c.block("pipeline", id, "pipeline definition missing")
			continue
		}
		rewritten, err := c.rewriteDoc(doc, false)
		if err != nil {
			c.block("pipeline", id, "pipeline definition could not be rewritten")
			continue
		}
		c.observeDoc(rewritten)
		destDoc, destFound, err := c.pipelineYAML(id, c.scope.Destination)
		if err != nil {
			return err
		}
		if !destFound {
			body := rewritten
			c.queue(Result{Kind: "pipeline", Identifier: id, Action: ActionCreate}, func() error {
				return c.postYAML("/pipeline/api/pipelines/v2", c.query(c.scope.Destination), body)
			})
			continue
		}
		destRewritten, rerr := c.rewriteDoc(destDoc, false)
		if rerr != nil || !sameDocument(rewritten, destRewritten) {
			c.conflictResult("pipeline", id)
			continue
		}
		c.keep(Result{Kind: "pipeline", Identifier: id, Action: ActionSkip, Reason: "destination matches"})
	}
	return nil
}

func (c *copier) pipelineYAML(id, project string) (string, bool, error) {
	obj, found, err := c.getObject("/pipeline/api/pipelines/"+url.PathEscape(id), c.query(project))
	if err != nil || !found {
		return "", false, err
	}
	data, _ := digMap(obj, "data")
	doc := stringField(data, "yamlPipeline", "yaml")
	if doc == "" {
		doc = stringField(obj, "yamlPipeline", "yaml")
	}
	return doc, doc != "", nil
}

func (c *copier) planInputSets(pipeline string) error {
	return c.planPipelineDocs("input_set", pipeline, "/pipeline/api/inputSets", "inputSetYaml", false)
}

func (c *copier) planTriggers(pipeline string) error {
	return c.planPipelineDocs("trigger", pipeline, "/pipeline/api/triggers", "yaml", true)
}

func (c *copier) planPipelineDocs(kind, pipeline, path, yamlKey string, disable bool) error {
	queryKey := "pipelineIdentifier"
	if kind == "trigger" {
		queryKey = "targetIdentifier"
	}
	page, size := "pageIndex", "pageSize"
	if kind == "trigger" {
		page, size = "page", "size"
	}
	items, err := c.list(http.MethodGet, path, mergeQuery(c.query(c.scope.Source), url.Values{queryKey: {pipeline}}), "", page, size, "")
	if err != nil {
		return err
	}
	for _, item := range items {
		id := stringField(item, "identifier")
		reportID := pipeline + "/" + id
		if isRemote(item) {
			c.skipGit(kind, reportID, "git-backed resource skipped")
			continue
		}
		doc := stringField(item, yamlKey, "yaml", "inputSetYaml")
		if doc == "" {
			obj, found, err := c.getObject(path+"/"+url.PathEscape(id), mergeQuery(c.query(c.scope.Source), url.Values{queryKey: {pipeline}}))
			if err != nil {
				return err
			}
			if !found {
				c.block(kind, reportID, "definition missing")
				continue
			}
			data, _ := digMap(obj, "data")
			doc = stringField(data, yamlKey, "yaml", "inputSetYaml")
		}
		rewritten, err := c.rewriteDoc(doc, disable)
		if err != nil || rewritten == "" {
			c.block(kind, reportID, "definition could not be rewritten")
			continue
		}
		c.observeDoc(rewritten)
		destObj, destFound, err := c.getObject(path+"/"+url.PathEscape(id), mergeQuery(c.query(c.scope.Destination), url.Values{queryKey: {pipeline}}))
		if err != nil {
			return err
		}
		if destFound {
			destData, _ := digMap(destObj, "data")
			destDoc := stringField(destData, yamlKey, "yaml", "inputSetYaml")
			destRewritten, rerr := c.rewriteDoc(destDoc, disable)
			if rerr != nil || !sameDocument(rewritten, destRewritten) {
				c.conflictResult(kind, reportID)
				continue
			}
			c.keep(Result{Kind: kind, Identifier: reportID, Action: ActionSkip, Reason: "destination matches"})
			continue
		}
		body := rewritten
		reason := ""
		if disable {
			reason = "created disabled"
		}
		c.queue(Result{Kind: kind, Identifier: reportID, Action: ActionCreate, Reason: reason}, func() error {
			return c.postYAML(path, mergeQuery(c.query(c.scope.Destination), url.Values{queryKey: {pipeline}}), body)
		})
	}
	return nil
}

func (c *copier) planPolicies() error {
	return c.planGovernance("policy", "/pm/api/v1/policies", func(item map[string]any) (map[string]any, string) {
		id := stringField(item, "identifier")
		return map[string]any{"identifier": id, "name": firstNonEmpty(stringField(item, "name"), id), "rego": stringField(item, "rego")}, id
	})
}

func (c *copier) planPolicySets() error {
	return c.planGovernance("policy_set", "/pm/api/v1/policysets", func(item map[string]any) (map[string]any, string) {
		id := stringField(item, "identifier")
		body := map[string]any{
			"identifier": id, "name": firstNonEmpty(stringField(item, "name"), id),
			"type": stringField(item, "type"), "action": stringField(item, "action"),
			"enabled": item["enabled"], "policies": item["policies"],
		}
		return body, id
	})
}

func (c *copier) planGovernance(kind, path string, bodyOf func(map[string]any) (map[string]any, string)) error {
	items, err := c.list(http.MethodGet, path, c.query(c.scope.Source), "", "page", "per_page", "")
	if err != nil {
		return err
	}
	for _, item := range items {
		if project := stringField(item, "projectIdentifier", "project_id"); project != "" && project != c.scope.Source {
			c.noteShared(kind + " " + stringField(item, "identifier"))
			continue
		}
		if regoMissing(kind, item) {
			full, found, err := c.getObject(path+"/"+url.PathEscape(stringField(item, "identifier")), c.query(c.scope.Source))
			if err != nil {
				return err
			}
			if found {
				if data, ok := digMap(full, "data"); ok {
					item = data
				} else {
					item = full
				}
			}
		}
		body, id := bodyOf(item)
		if id == "" {
			c.block(kind, "", kind+" identifier missing")
			continue
		}
		dest, found, err := c.getObject(path+"/"+url.PathEscape(id), c.query(c.scope.Destination))
		if err != nil {
			return err
		}
		if found {
			got := dest
			if data, ok := digMap(dest, "data"); ok {
				got = data
			}
			gotBody, _ := bodyOf(got)
			if canonicalJSON(gotBody) == canonicalJSON(body) {
				c.keep(Result{Kind: kind, Identifier: id, Action: ActionSkip, Reason: "destination matches"})
				continue
			}
			c.conflictResult(kind, id)
			continue
		}
		raw, _ := json.Marshal(body)
		payload := string(raw)
		c.queue(Result{Kind: kind, Identifier: id, Action: ActionCreate}, func() error {
			return c.postJSON(path, c.query(c.scope.Destination), payload)
		})
	}
	return nil
}

func regoMissing(kind string, item map[string]any) bool {
	if kind != "policy" {
		return item["policies"] == nil
	}
	return stringField(item, "rego") == ""
}

func (c *copier) planDelegates() error {
	if len(c.selectors) == 0 {
		return nil
	}
	items, err := c.list(http.MethodPost, "/ng/api/delegate-setup/listDelegates", c.query(""), `{"filterType":"Delegate"}`, "page", "size", "")
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

func (c *copier) planChildren(kind string, parents []string, fn func(string) error) error {
	for _, parent := range parents {
		if err := fn(parent); err != nil {
			return err
		}
	}
	return nil
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

func (c *copier) query(project string) url.Values {
	q := url.Values{"accountIdentifier": {c.scope.Account}, "orgIdentifier": {c.scope.Org}}
	if project != "" {
		q.Set("projectIdentifier", project)
	}
	return q
}

func mergeQuery(base, extra url.Values) url.Values {
	out := url.Values{}
	for key, values := range base {
		for _, value := range values {
			out.Add(key, value)
		}
	}
	for key, values := range extra {
		for _, value := range values {
			out.Set(key, value)
		}
	}
	return out
}

func (c *copier) getObject(path string, query url.Values) (map[string]any, bool, error) {
	status, body, err := c.transport.exchange(c.ctx, http.MethodGet, path, query, "", "")
	if err != nil {
		return nil, false, err
	}
	if isNotFound(status, body) {
		return nil, false, nil
	}
	if status != http.StatusOK {
		return nil, false, fmt.Errorf("project copy read failed: status %d", status)
	}
	if len(body) == 0 {
		return nil, false, errors.New("project copy read returned an empty body")
	}
	var raw any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, false, errors.New("malformed project copy response")
	}
	obj, ok := asMap(raw)
	if !ok {
		return nil, false, errors.New("malformed project copy response")
	}
	if statusField := stringField(obj, "status"); statusField != "" && !strings.EqualFold(statusField, "SUCCESS") {
		if isNotFound(status, body) {
			return nil, false, nil
		}
		return nil, false, errors.New("project copy read failed")
	}
	return obj, true, nil
}

func (c *copier) postJSON(path string, query url.Values, body string) error {
	method := http.MethodPost
	if strings.Contains(path, "updateStableTemplate") {
		method = http.MethodPut
	}
	return c.write(method, path, query, body, "application/json")
}

func (c *copier) postYAML(path string, query url.Values, body string) error {
	if query == nil {
		query = c.query(c.scope.Destination)
	}
	return c.write(http.MethodPost, path, query, body, "application/yaml")
}

// postCDYAML posts service/environment/infrastructure YAML in the NG JSON envelope
// {identifier, name, orgIdentifier, projectIdentifier, yaml: "<resource yaml>"}.
func (c *copier) postCDYAML(kind, path string, extra url.Values, doc string) error {
	env, err := cdEnvelope(kind, doc)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return err
	}
	query := c.query(c.scope.Destination)
	if extra != nil {
		query = mergeQuery(query, extra)
	}
	return c.postJSON(path, query, string(raw))
}

func cdEnvelope(wrapper, rawBody string) (map[string]any, error) {
	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(rawBody), &parsed); err != nil || parsed == nil {
		return nil, errors.New("definition could not be wrapped for create")
	}
	plain, _ := yamlToPlain(parsed).(map[string]any)
	if plain == nil {
		return nil, errors.New("definition could not be wrapped for create")
	}
	inner := plain
	if wrapped, ok := plain[wrapper].(map[string]any); ok {
		inner = wrapped
	}
	env := map[string]any{"yaml": rawBody}
	for _, key := range []string{"identifier", "name", "orgIdentifier", "projectIdentifier", "description", "tags", "type", "environmentRef"} {
		if value, ok := inner[key]; ok {
			env[key] = value
		}
	}
	return env, nil
}

func (c *copier) write(method, path string, query url.Values, body, contentType string) error {
	status, resp, err := c.transport.exchange(c.ctx, method, path, query, body, contentType)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return writeFailure(status, resp)
	}
	return nil
}

func writeFailure(status int, body []byte) error {
	msg := fmt.Sprintf("project copy write failed: status %d", status)
	var obj map[string]any
	if json.Unmarshal(body, &obj) != nil {
		return errors.New(msg)
	}
	if code := stringField(obj, "code"); code != "" && len(code) < 80 {
		msg += " " + code
	}
	if message := stringField(obj, "message"); message != "" && len(message) < 240 {
		msg += ": " + message
	}
	return errors.New(msg)
}

func (c *copier) list(method, path string, query url.Values, body, pageParam, sizeParam, wrap string) ([]map[string]any, error) {
	var all []map[string]any
	seen := map[string]bool{}
	for page := 0; page < 1000; page++ {
		q := mergeQuery(query, url.Values{pageParam: {strconv.Itoa(page)}, sizeParam: {"100"}})
		status, data, err := c.transport.exchange(c.ctx, method, path, q, body, "application/json")
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("project copy list failed: status %d", status)
		}
		pageData, err := extractItems(data)
		if err != nil {
			return nil, err
		}
		items := pageData.Items
		if wrap != "" {
			unwrapped := make([]map[string]any, 0, len(items))
			for _, item := range items {
				if inner, ok := asMap(item[wrap]); ok {
					unwrapped = append(unwrapped, inner)
					continue
				}
				unwrapped = append(unwrapped, item)
			}
			items = unwrapped
		}
		fresh := 0
		for _, item := range items {
			id := stringField(item, "identifier")
			version := stringField(item, "versionLabel", "version")
			key := id + "/" + version
			if id == "" {
				key = canonicalJSON(item)
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			fresh++
			all = append(all, item)
		}
		if len(all) > 100000 {
			return nil, errors.New("project copy list exceeded 100000 items")
		}
		more := false
		switch {
		case pageData.HasTotalPages:
			more = page+1 < pageData.TotalPages
			if !more && pageData.HasTotalItems && len(all) != pageData.TotalItems {
				return nil, errors.New("project copy list incomplete")
			}
		case pageData.HasTotalItems:
			more = len(all) < pageData.TotalItems
		default:
			more = len(items) == 100
		}
		if !more {
			return all, nil
		}
		if len(items) == 0 || fresh == 0 {
			return nil, errors.New("project copy list incomplete")
		}
	}
	return nil, errors.New("project copy list exceeded page limit")
}

type listPage struct {
	Items         []map[string]any
	TotalPages    int
	TotalItems    int
	HasTotalPages bool
	HasTotalItems bool
}

func extractItems(body []byte) (listPage, error) {
	if len(body) == 0 {
		return listPage{}, errors.New("project copy list returned an empty body")
	}
	var top any
	if err := json.Unmarshal(body, &top); err != nil {
		return listPage{}, errors.New("malformed project copy list")
	}
	switch node := top.(type) {
	case []any:
		items := mapsOf(node)
		return listPage{Items: items, TotalPages: 1, HasTotalPages: true, TotalItems: len(items), HasTotalItems: true}, nil
	case map[string]any:
		if status := stringField(node, "status"); status != "" && !strings.EqualFold(status, "SUCCESS") {
			return listPage{}, errors.New("project copy list failed")
		}
		data := node
		if raw, exists := node["data"]; exists {
			inner, ok := asMap(raw)
			if !ok {
				return listPage{}, errors.New("unrecognized project copy list")
			}
			data = inner
		}
		var items []any
		found := false
		for _, key := range []string{"content", "policies", "policy_sets", "policysets", "resource"} {
			if arr, ok := asSlice(data[key]); ok {
				items, found = arr, true
				break
			}
			if arr, ok := asSlice(node[key]); ok {
				items, found = arr, true
				break
			}
		}
		if !found {
			return listPage{}, errors.New("unrecognized project copy list")
		}
		page := listPage{Items: mapsOf(items)}
		if _, ok := data["totalPages"]; ok {
			page.HasTotalPages, page.TotalPages = true, intFrom(data["totalPages"])
		} else if _, ok := node["totalPages"]; ok {
			page.HasTotalPages, page.TotalPages = true, intFrom(node["totalPages"])
		}
		if v, ok := data["totalItems"]; ok {
			page.HasTotalItems, page.TotalItems = true, intFrom(v)
		} else if v, ok := data["totalElements"]; ok {
			page.HasTotalItems, page.TotalItems = true, intFrom(v)
		}
		return page, nil
	default:
		return listPage{}, errors.New("unrecognized project copy list")
	}
}

func mapsOf(items []any) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if m, ok := asMap(item); ok {
			out = append(out, m)
		}
	}
	return out
}

func digMap(obj map[string]any, keys ...string) (map[string]any, bool) {
	var current any = obj
	for _, key := range keys {
		m, ok := asMap(current)
		if !ok {
			return nil, false
		}
		current = m[key]
	}
	return asMap(current)
}

func isNotFound(status int, body []byte) bool {
	if status == http.StatusNotFound {
		return true
	}
	var envelope struct {
		Status string `json:"status"`
		Code   string `json:"code"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return false
	}
	if strings.EqualFold(envelope.Status, "SUCCESS") {
		return false
	}
	code := strings.ToUpper(strings.TrimSpace(envelope.Code))
	return code == "ENTITY_NOT_FOUND" || code == "RESOURCE_NOT_FOUND_EXCEPTION" || strings.HasSuffix(code, "_NOT_FOUND")
}

func intFrom(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return 0
	}
}

func truthy(v any) bool {
	switch n := v.(type) {
	case bool:
		return n
	case string:
		return strings.EqualFold(n, "true")
	default:
		return false
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
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
