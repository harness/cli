// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package projectcopy

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"strings"

	"github.com/harness/cli/v3/pkg/client"
	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/registry"
	"github.com/harness/cli/v3/pkg/spec"
)

// listed is one row from a source or destination list.
type listed struct {
	ID      string
	Parent  string
	Version string
	Doc     string
	Stable  bool
	Raw     map[string]any
}

// got is one resource read. Found is false when the API reports it missing.
type got struct {
	Found   bool
	Doc     string
	Version string
	Stable  bool
	Raw     map[string]any
}

// Gateway is the list/get/create surface the copy loop uses. The production
// implementation calls the existing command specs. Tests supply a fake.
type Gateway interface {
	List(noun, project, parent string) ([]listed, error)
	Get(noun, project, id, parent string) (got, error)
	Create(noun, project, id, parent, body string) error
	SetStable(project, templateID, version string) error
	Delegates() ([]map[string]any, error)
}

type specGateway struct {
	ctx *cmdctx.Ctx
}

func newSpecGateway(ctx *cmdctx.Ctx) Gateway {
	return &specGateway{ctx: ctx}
}

func (g *specGateway) List(noun, project, parent string) ([]listed, error) {
	cs, child, err := g.prepare("list", noun, project, "", parent, "")
	if err != nil {
		return nil, err
	}
	if noun == "template" {
		child.SetFlag("list-type", "ALL")
	}
	items, err := g.ctx.Resolver.FetchItems(child, cs.Endpoint, cmdctx.PagingFlags{All: true})
	if err != nil {
		return nil, err
	}
	out := make([]listed, 0, len(items))
	for _, item := range items {
		if row, ok := toListed(noun, item); ok {
			out = append(out, row)
		}
	}
	return out, nil
}

func (g *specGateway) Get(noun, project, id, parent string) (got, error) {
	cs, child, err := g.prepare("get", noun, project, id, parent, "")
	if err != nil {
		return got{}, err
	}
	result, err := registry.CallEndpoint(child, cs.Endpoint)
	if err != nil {
		if notFoundErr(err) {
			return got{}, nil
		}
		return got{}, err
	}
	raw, _ := asMap(result)
	if raw == nil {
		return got{Found: true}, nil
	}
	encoded, _ := json.Marshal(raw)
	if isNotFound(200, encoded) {
		return got{}, nil
	}
	if status := stringField(raw, "status"); status != "" && !strings.EqualFold(status, "SUCCESS") {
		return got{}, errors.New("project copy read failed")
	}
	entity := unwrapResource(raw)
	return got{
		Found:   true,
		Raw:     entity,
		Doc:     docFrom(raw, entity),
		Version: versionOf(entity),
		Stable:  stableOf(entity),
	}, nil
}

func (g *specGateway) Create(noun, project, id, parent, body string) error {
	cs, child, err := g.prepare("create", noun, project, id, parent, body)
	if err != nil {
		return err
	}
	_, err = registry.CallEndpoint(child, cs.Endpoint)
	return err
}

func (g *specGateway) SetStable(project, templateID, version string) error {
	cs := g.ctx.Resolver.GetSpec("update", "template_version:set-stable")
	if cs == nil || cs.Endpoint == nil {
		return errors.New("no spec for update template_version:set-stable")
	}
	child := g.child(project)
	child.Verb = "update"
	child.Noun = "template_version:set-stable"
	child.Id = templateID + "/" + version
	child.IdParts = []string{templateID, version}
	_, err := registry.CallEndpoint(&child, cs.Endpoint)
	return err
}

func (g *specGateway) Delegates() ([]map[string]any, error) {
	rows, err := g.List("delegate", "", "")
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Raw)
	}
	return out, nil
}

func (g *specGateway) prepare(verb, noun, project, id, parent, body string) (*spec.CommandSpec, *cmdctx.Ctx, error) {
	specNoun := noun
	switch noun {
	case "template_version":
		specNoun = "template_version"
		if verb == "get" {
			verb = "get"
		}
	}
	cs := g.ctx.Resolver.GetSpec(verb, specNoun)
	if cs == nil || cs.Endpoint == nil {
		return nil, nil, fmt.Errorf("no spec for %s %s", verb, specNoun)
	}
	child := g.child(project)
	child.Verb = verb
	child.Noun = specNoun
	switch noun {
	case "trigger", "input_set":
		if verb == "list" || verb == "create" {
			child.Id = parent
			child.ParentId = parent
		} else {
			child.Id = parent + "/" + id
			child.IdParts = []string{parent, id}
		}
	case "infrastructure", "service_override":
		child.Id = id
		child.ParentId = parent
		if parent != "" {
			child.SetFlag("env", parent)
		}
	case "template_version":
		child.ParentId = parent
		child.Id = parent + "/" + id
		child.IdParts = []string{parent, id}
	case "template":
		child.Id = id
		child.ParentId = parent
	default:
		child.Id = id
		child.ParentId = parent
	}
	if body != "" {
		child.InlineBody = body
	}
	return cs, &child, nil
}

func (g *specGateway) child(project string) cmdctx.Ctx {
	c := *g.ctx
	authCopy := *g.ctx.Auth
	authCopy.ProjectID = project
	c.Auth = &authCopy
	c.FlagValues = maps.Clone(g.ctx.FlagValues)
	if c.FlagValues == nil {
		c.FlagValues = map[string]any{}
	}
	c.ProvidedFlags = maps.Clone(g.ctx.ProvidedFlags)
	if c.ProvidedFlags == nil {
		c.ProvidedFlags = map[string]bool{}
	}
	c.InlineBody = nil
	c.SetArgs = nil
	c.DelArgs = nil
	c.FormatFlags = cmdctx.FormatFlags{}
	c.RequestPreview = nil
	return c
}

func toListed(noun string, item any) (listed, bool) {
	raw, ok := asMap(item)
	if !ok {
		return listed{}, false
	}
	entity := unwrapResource(raw)
	row := listed{
		ID:      stringField(entity, "identifier"),
		Raw:     entity,
		Doc:     docFrom(raw, entity),
		Version: versionOf(entity),
		Stable:  stableOf(entity),
	}
	if noun == "template_version" && row.Version != "" {
		row.ID = row.Version
	}
	if row.ID == "" {
		row.ID = stringField(entity, "name", "groupName", "delegateGroupIdentifier")
	}
	return row, row.ID != ""
}

func unwrapResource(raw map[string]any) map[string]any {
	cur := raw
	if data, ok := asMap(raw["data"]); ok {
		cur = data
	}
	for _, key := range []string{"connector", "secret", "service", "environment", "infrastructure", "project", "template", "pipeline"} {
		if inner, ok := asMap(cur[key]); ok {
			return inner
		}
	}
	return cur
}

func docFrom(raw, entity map[string]any) string {
	for _, candidate := range []map[string]any{entity, raw} {
		if candidate == nil {
			continue
		}
		if doc := stringField(candidate, "yaml", "yamlPipeline", "inputSetYaml", "pipeline_yaml", "template_yaml"); doc != "" {
			return doc
		}
		if data, ok := asMap(candidate["data"]); ok {
			if doc := stringField(data, "yaml", "yamlPipeline", "inputSetYaml", "pipeline_yaml", "template_yaml"); doc != "" {
				return doc
			}
		}
	}
	return ""
}

func versionOf(raw map[string]any) string {
	if raw == nil {
		return ""
	}
	return stringField(raw, "versionLabel", "version", "version_label")
}

func stableOf(raw map[string]any) bool {
	if raw == nil {
		return false
	}
	return truthy(raw["stableTemplate"]) || truthy(raw["stable"]) || truthy(raw["stable_template"])
}

func notFoundErr(err error) bool {
	var api *client.APIError
	if !errors.As(err, &api) {
		return false
	}
	return isNotFound(api.Status, api.Body)
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
