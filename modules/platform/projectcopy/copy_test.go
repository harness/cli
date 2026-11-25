// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package projectcopy

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestCopyAppliesCDCoreAndSkipsIdenticalRerun(t *testing.T) {
	gw := newFixture(false)
	report, err := runCopy(testScope, true, gw)
	if err == nil || !strings.Contains(err.Error(), "git-backed") {
		t.Fatalf("expected incomplete copy for skipped git-backed resources: %v", err)
	}
	if !report.InventoryComplete || !strings.Contains(report.Readiness, "skipped") {
		t.Fatalf("readiness %q", report.Readiness)
	}
	raw, _ := json.Marshal(report)
	for _, leaked := range []string{"SUPER_SECRET", "vault/path#key"} {
		if strings.Contains(string(raw), leaked) {
			t.Fatalf("report leaked %s", leaked)
		}
	}
	if !containsResult(report, "pipeline", "gitpipe", ActionSkipped) {
		t.Fatal("git-backed pipeline was not skipped")
	}
	if !containsResult(report, "reference", "account.shared_connector", ActionShared) {
		t.Fatal("account connector was copied instead of reported")
	}
	if !containsResult(report, "reference", "project.other_project", ActionShared) {
		t.Fatal("pipeline stage project reference was not reported")
	}
	if !containsResult(report, "infrastructure", "prod/k8s", ActionCreate) {
		t.Fatal("infrastructure was not planned")
	}
	if !containsResult(report, "service_override", "prod/ov", ActionCreate) {
		t.Fatal("service override was not planned")
	}
	if !containsResult(report, "secret", "db_password", ActionSkipped) || !strings.Contains(strings.Join(report.SecretsPendingValue, ","), "db_password") {
		t.Fatal("inline secret was created instead of reported")
	}
	if len(report.DelegateSelectors) != 1 || !strings.Contains(report.DelegateSelectors[0].Reason, "harness-delegate") {
		t.Fatalf("delegate check %#v", report.DelegateSelectors)
	}
	var projectBody, triggerBody, pipelineBody, secretBody string
	for _, call := range gw.writes {
		switch {
		case call.Noun == "project":
			projectBody = call.Body
		case call.Noun == "trigger":
			triggerBody = call.Body
		case call.Noun == "pipeline" && call.ID == "deploy":
			pipelineBody = call.Body
		case call.Noun == "secret":
			secretBody = call.Body
		}
	}
	if strings.Contains(secretBody, "SUPER_SECRET") || strings.Contains(secretBody, "db_password") {
		t.Fatalf("inline secret was written: %s", secretBody)
	}
	if !strings.Contains(secretBody, "ref_vault") || !strings.Contains(secretBody, "vault/path#key") {
		t.Fatalf("reference secret body = %s", secretBody)
	}
	if !strings.Contains(triggerBody, "enabled: false") {
		t.Fatalf("trigger body = %s", triggerBody)
	}
	if !strings.Contains(pipelineBody, "project: "+testScope.Destination) || !strings.Contains(pipelineBody, "project: other_project") || !strings.Contains(pipelineBody, "enabled: true") || !strings.Contains(pipelineBody, "org.echo") || !strings.Contains(pipelineBody, "account.shared_connector") {
		t.Fatalf("pipeline body = %s", pipelineBody)
	}
	if !strings.Contains(projectBody, `"identifier":"`+testScope.Destination+`"`) || !strings.Contains(projectBody, `"name":"`+testScope.Destination+`"`) || strings.Contains(projectBody, `"name":"Source"`) || !strings.Contains(projectBody, "CD") {
		t.Fatalf("project body = %s", projectBody)
	}
	for _, call := range gw.writes {
		if call.Noun == "delegate" || call.Noun == "connector" && call.ID == "harnessSecretManager" {
			t.Fatalf("unexpected write %#v", call)
		}
	}
	if !writeBefore(gw, "connector", "vault", "secret", "ref_vault") || !writeBefore(gw, "secret", "ref_vault", "connector", "github") {
		t.Fatalf("secret manager was not created before the reference secret: %#v", gw.writes)
	}
	if !writeBefore(gw, "environment", "prod", "infrastructure", "k8s") || !writeBefore(gw, "template", "parent", "template", "child") {
		t.Fatalf("dependency order %#v", gw.writes)
	}
	before := len(gw.writes)
	again, err := runCopy(testScope, true, gw)
	if err == nil || !strings.Contains(err.Error(), "git-backed") {
		t.Fatalf("expected incomplete rerun for skipped git-backed resources: %v", err)
	}
	if len(gw.writes) != before {
		t.Fatalf("rerun wrote %d extra resources", len(gw.writes)-before)
	}
	if !containsResult(again, "secret", "ref_vault", ActionSkip) || !containsResult(again, "pipeline", "deploy", ActionSkip) || !containsResult(again, "infrastructure", "prod/k8s", ActionSkip) || !containsResult(again, "service_override", "prod/ov", ActionSkip) {
		t.Fatal("identical rerun did not skip")
	}
}

func TestCopyPlanDoesNotWrite(t *testing.T) {
	gw := newFixture(false)
	report, err := runCopy(testScope, false, gw)
	if err == nil || !strings.Contains(err.Error(), "git-backed") {
		t.Fatalf("expected incomplete plan for skipped git-backed resources: %v", err)
	}
	if len(gw.writes) != 0 {
		t.Fatalf("plan wrote %#v", gw.writes)
	}
	if !containsResult(report, "secret", "db_password", ActionSkipped) || !strings.Contains(strings.Join(report.SecretsPendingValue, ","), "db_password") {
		t.Fatal("plan did not list the inline secret")
	}
	if !containsResult(report, "infrastructure", "prod/k8s", ActionCreate) || !containsResult(report, "service_override", "prod/ov", ActionCreate) {
		t.Fatal("plan missed infrastructure or service override")
	}
}

func TestCopyConflictWritesNothing(t *testing.T) {
	gw := newFixture(true)
	report, err := runCopy(testScope, true, gw)
	if err == nil || !strings.Contains(err.Error(), "nothing written") && !strings.Contains(err.Error(), "differs") {
		t.Fatalf("err=%v", err)
	}
	if len(gw.writes) != 0 {
		t.Fatalf("conflict wrote %#v", gw.writes)
	}
	if !containsResult(report, "pipeline", "deploy", ActionConflict) {
		t.Fatal("conflict missing from report")
	}
}

func TestCopyResumesAfterCreateFailure(t *testing.T) {
	gw := newFixture(false)
	gw.failNoun = "service"
	report, err := runCopy(testScope, true, gw)
	if err == nil || report.Readiness != "copy stopped after a create failure; re-run to continue" {
		t.Fatalf("err=%v readiness=%q", err, report.Readiness)
	}
	if containsResult(report, "service", "svc", ActionCreate) {
		t.Fatal("failed create recorded as success")
	}
	if writesOf(gw, "project") != 1 || writesOf(gw, "service") != 0 {
		t.Fatalf("writes %#v", gw.writes)
	}
	again, err := runCopy(testScope, true, gw)
	if err == nil || !strings.Contains(err.Error(), "git-backed") {
		t.Fatalf("expected resume to finish remaining writes: %v", err)
	}
	if !containsResult(again, "project", testScope.Destination, ActionSkip) || !containsResult(again, "service", "svc", ActionCreate) {
		t.Fatalf("resume results %#v", again.Results)
	}
	if writesOf(gw, "project") != 1 {
		t.Fatalf("resume recreated the project")
	}
}

func TestCopyStableTemplateFailureRetriesOnRerun(t *testing.T) {
	gw := &memGW{by: map[string][]memItem{}}
	gw.add("project", testScope.Source, memItem{ID: testScope.Source, Raw: map[string]any{"identifier": testScope.Source, "name": "Source", "orgIdentifier": testScope.Org}})
	doc := "template:\n  identifier: step\n  name: step\n  versionLabel: v1\n  type: Step\n  orgIdentifier: " + testScope.Org + "\n  projectIdentifier: " + testScope.Source + "\n  spec: {}\n"
	gw.add("template", testScope.Source, memItem{ID: "step"})
	gw.add("template_version", testScope.Source, memItem{ID: "v1", Parent: "step", Version: "v1", Stable: true, Doc: doc})
	gw.failStable = true
	report, err := runCopy(testScope, true, gw)
	if err == nil || report.Readiness != "copy stopped after a create failure; re-run to continue" {
		t.Fatalf("err=%v readiness=%q", err, report.Readiness)
	}
	if !containsResult(report, "template", "step/v1", ActionBlocked) {
		t.Fatalf("stable failure %#v", report.Results)
	}
	if writesOf(gw, "template") != 1 || writesOf(gw, "template:stable") != 0 {
		t.Fatalf("writes %#v", gw.writes)
	}
	gw.failStable = false
	again, err := runCopy(testScope, true, gw)
	if err != nil {
		t.Fatal(err)
	}
	if writesOf(gw, "template") != 1 || writesOf(gw, "template:stable") != 1 {
		t.Fatalf("rerun did not retry only the stable update: %#v", gw.writes)
	}
	if !containsResult(again, "template", "step/v1", ActionCreate) || again.Results[indexResult(again, "template", "step/v1")].Reason != "set stable version" {
		t.Fatalf("rerun results %#v", again.Results)
	}
}

func TestCopyAcceptsLiveInlineSecretSchema(t *testing.T) {
	s := &secret{Identifier: "github-pat", Name: "github-pat", Org: testScope.Org, Project: testScope.Source, Type: "SecretText", Spec: map[string]json.RawMessage{
		"secretManagerIdentifier": json.RawMessage(`"harnessSecretManager"`),
		"valueType":               json.RawMessage(`"Inline"`),
		"value":                   json.RawMessage(`null`),
		"additionalMetadata":      json.RawMessage(`null`),
	}}
	if classify(s).Capability != MappingRequired {
		t.Fatal(classify(s))
	}
}

func TestRewriteDisablesOnlyTriggerDocument(t *testing.T) {
	doc, err := disableTriggerDoc("trigger:\n  identifier: push\n  enabled: true\n  pipelineIdentifier: deploy\n  projectIdentifier: source\n")
	if err != nil {
		t.Fatal(err)
	}
	rewritten, err := rewriteYAML(doc, rewriteOpts{Org: testScope.Org, Project: testScope.Destination, SourceOrg: testScope.Org, SourceProject: testScope.Source})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rewritten, "projectIdentifier: "+testScope.Destination) || !strings.Contains(rewritten, "enabled: false") {
		t.Fatal(rewritten)
	}
	pipeline, err := rewriteYAML("pipeline:\n  identifier: deploy\n  enabled: true\n  pipelineIdentifier: deploy\n  projectIdentifier: source\n", rewriteOpts{Org: testScope.Org, Project: testScope.Destination, SourceOrg: testScope.Org, SourceProject: testScope.Source})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pipeline, "enabled: true") {
		t.Fatal(pipeline)
	}
	if !sameTrigger("trigger:\n  identifier: push\n  enabled: true\n", "trigger:\n  identifier: push\n  enabled: false\n") {
		t.Fatal("enabled flag counted as a conflict")
	}
}

func TestRewritePipelineStageProject(t *testing.T) {
	doc, err := rewriteYAML("pipeline:\n  projectIdentifier: source\n  stages:\n    - stage:\n        type: Pipeline\n        spec:\n          org: "+testScope.Org+"\n          project: source\n          pipeline: child\n    - stage:\n        type: Pipeline\n        spec:\n          project: other_project\n          pipeline: foreign\n", rewriteOpts{Org: testScope.Org, Project: testScope.Destination, SourceOrg: testScope.Org, SourceProject: testScope.Source})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc, "project: "+testScope.Destination) || !strings.Contains(doc, "project: other_project") || strings.Contains(doc, "project: source") {
		t.Fatal(doc)
	}
	if !strings.Contains(doc, "account.kept") && strings.Contains(doc, "projectIdentifier: source") {
		t.Fatal(doc)
	}
}

func TestRewritePreservesScopedRefs(t *testing.T) {
	doc, err := rewriteYAML("pipeline:\n  projectIdentifier: source\n  connectorRef: account.shared_connector\n  orgRef: org.echo\n", rewriteOpts{Org: testScope.Org, Project: testScope.Destination, SourceOrg: testScope.Org, SourceProject: testScope.Source})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc, "account.shared_connector") || !strings.Contains(doc, "org.echo") || strings.Contains(doc, "projectIdentifier: source") {
		t.Fatal(doc)
	}
}

func TestSecretMetaMatchesReferencePath(t *testing.T) {
	want := map[string]any{"name": "ref", "type": "SecretText", "spec": map[string]any{"valueType": "Reference", "secretManagerIdentifier": "org.manager", "value": "vault/path#key"}}
	got := map[string]any{"name": "ref", "type": "SecretText", "spec": map[string]any{"valueType": "Reference", "secretManagerIdentifier": "org.manager", "value": "vault/other#key"}}
	if secretMetaMatches(want, got) {
		t.Fatal("different reference paths treated as identical")
	}
	got["spec"].(map[string]any)["value"] = "vault/path#key"
	if !secretMetaMatches(want, got) {
		t.Fatal("matching reference path rejected")
	}
}

func TestOrderTemplateUnitsCopiesDependenciesFirst(t *testing.T) {
	ordered := orderTemplateUnits([]templateUnit{
		{id: "child", version: "v1", doc: "template:\n  spec:\n    templateRef: parent\n"},
		{id: "parent", version: "v2", doc: "template:\n  spec:\n    templateRef: account.echo\n"},
		{id: "parent", version: "v1", doc: "template:\n  spec: {}\n"},
	})
	if len(ordered) != 3 || ordered[0].id != "parent" || ordered[0].version != "v1" || ordered[1].id != "parent" || ordered[1].version != "v2" || ordered[2].id != "child" {
		t.Fatalf("%#v", ordered)
	}
}

func TestDelegateConnectedRejectsDisconnected(t *testing.T) {
	if delegateConnected(map[string]any{"connectivityStatus": "DISCONNECTED"}) {
		t.Fatal("DISCONNECTED accepted")
	}
	if !delegateConnected(map[string]any{"connectivityStatus": "connected"}) {
		t.Fatal("connected rejected")
	}
}

func TestIsNotFoundUsesAPICode(t *testing.T) {
	if !isNotFound(http.StatusNotFound, nil) {
		t.Fatal("404")
	}
	if !isNotFound(http.StatusOK, []byte(`{"status":"ERROR","code":"RESOURCE_NOT_FOUND_EXCEPTION"}`)) {
		t.Fatal("API not-found code")
	}
	if isNotFound(http.StatusBadRequest, []byte("resource NOT_FOUND in yaml")) {
		t.Fatal("substring match")
	}
	if isNotFound(http.StatusOK, []byte(`{"status":"SUCCESS","code":"RESOURCE_NOT_FOUND"}`)) {
		t.Fatal("success envelope")
	}
}

func containsResult(report Report, kind, id string, action Action) bool {
	return indexResult(report, kind, id) >= 0 && report.Results[indexResult(report, kind, id)].Action == action
}

func indexResult(report Report, kind, id string) int {
	for i, item := range report.Results {
		if item.Kind == kind && item.Identifier == id {
			return i
		}
	}
	return -1
}

func writeBefore(gw *memGW, leftNoun, leftID, rightNoun, rightID string) bool {
	left, right := -1, -1
	for i, call := range gw.writes {
		if call.Noun == leftNoun && call.ID == leftID && left < 0 {
			left = i
		}
		if call.Noun == rightNoun && call.ID == rightID && right < 0 {
			right = i
		}
	}
	return left >= 0 && right >= 0 && left < right
}

func writesOf(gw *memGW, noun string) int {
	n := 0
	for _, call := range gw.writes {
		if call.Noun == noun {
			n++
		}
	}
	return n
}

type gwWrite struct {
	Noun, ID, Parent, Project, Body string
}

type memItem struct {
	ID, Parent, Doc, Version string
	Stable, Remote           bool
	Raw                      map[string]any
}

type memGW struct {
	by         map[string][]memItem
	writes     []gwWrite
	failNoun   string
	didFail    bool
	failStable bool
	delegates  []map[string]any
}

func (g *memGW) add(noun, project string, item memItem) {
	if item.Raw == nil {
		item.Raw = map[string]any{}
	}
	if item.ID != "" && stringField(item.Raw, "identifier") == "" {
		item.Raw["identifier"] = item.ID
	}
	if item.Remote {
		item.Raw["storeType"] = "REMOTE"
	}
	if item.Doc != "" {
		item.Raw["yaml"] = item.Doc
	}
	if item.Version != "" {
		item.Raw["versionLabel"] = item.Version
	}
	if item.Stable {
		item.Raw["stableTemplate"] = true
	}
	g.by[noun+"|"+project] = append(g.by[noun+"|"+project], item)
}

func (g *memGW) List(noun, project, parent string) ([]listed, error) {
	var out []listed
	for _, item := range g.by[noun+"|"+project] {
		if parent != "" && item.Parent != parent {
			continue
		}
		out = append(out, listed{ID: item.ID, Parent: item.Parent, Version: item.Version, Doc: item.Doc, Stable: item.Stable, Raw: cloneMap(item.Raw)})
	}
	return out, nil
}

func (g *memGW) Get(noun, project, id, parent string) (got, error) {
	for _, item := range g.by[noun+"|"+project] {
		if item.ID != id {
			continue
		}
		if parent != "" && item.Parent != "" && item.Parent != parent {
			continue
		}
		return got{Found: true, Doc: item.Doc, Raw: cloneMap(item.Raw), Version: item.Version, Stable: item.Stable}, nil
	}
	return got{}, nil
}

func (g *memGW) Create(noun, project, id, parent, body string) error {
	if g.failNoun == noun && !g.didFail {
		g.didFail = true
		return errors.New("create failed")
	}
	g.writes = append(g.writes, gwWrite{Noun: noun, ID: id, Parent: parent, Project: project, Body: body})
	item := memItem{ID: id, Parent: parent, Doc: body}
	if strings.HasPrefix(strings.TrimSpace(body), "{") {
		var raw map[string]any
		if json.Unmarshal([]byte(body), &raw) == nil {
			item.Raw = raw
			item.Doc = ""
		}
	}
	g.add(noun, project, item)
	if noun == "template" {
		ver := versionLabel(body)
		g.add("template_version", project, memItem{ID: ver, Parent: id, Version: ver, Doc: body})
	}
	return nil
}

func (g *memGW) SetStable(project, id, version string) error {
	if g.failStable {
		return errors.New("stable update failed")
	}
	g.writes = append(g.writes, gwWrite{Noun: "template:stable", ID: id + "/" + version, Project: project})
	items := g.by["template_version|"+project]
	for i := range items {
		if items[i].Parent == id && (items[i].Version == version || items[i].ID == version) {
			items[i].Stable = true
			items[i].Raw["stableTemplate"] = true
		}
	}
	return nil
}

func (g *memGW) Delegates() ([]map[string]any, error) {
	return g.delegates, nil
}

func newFixture(conflict bool) *memGW {
	org := testScope.Org
	src := testScope.Source
	gw := &memGW{by: map[string][]memItem{}, delegates: []map[string]any{{
		"groupName": "harness-delegate", "delegateGroupIdentifier": "del1", "connectivityStatus": "connected", "tags": []any{"cd-delegate"},
	}}}
	gw.add("project", src, memItem{ID: src, Raw: map[string]any{"identifier": src, "name": "Source", "orgIdentifier": org, "description": "demo", "color": "#004fc4", "modules": []any{"CD"}}})
	secret := func(id, name, valueType, manager, value string) {
		gw.add("secret", src, memItem{ID: id, Raw: map[string]any{
			"identifier": id, "name": name, "orgIdentifier": org, "projectIdentifier": src, "type": "SecretText",
			"spec": map[string]any{"type": "SecretText", "valueType": valueType, "secretManagerIdentifier": manager, "value": value},
		}})
	}
	secret("db_password", "db", "Inline", "harnessSecretManager", "SUPER_SECRET")
	secret("ref_vault", "ref", "Reference", "vault", "vault/path#key")
	gw.add("connector", src, memItem{ID: "vault", Raw: map[string]any{"identifier": "vault", "name": "vault", "type": "Vault", "orgIdentifier": org, "projectIdentifier": src, "spec": map[string]any{}}})
	gw.add("connector", src, memItem{ID: "github", Raw: map[string]any{"identifier": "github", "name": "github", "type": "Github", "orgIdentifier": org, "projectIdentifier": src, "spec": map[string]any{"delegateSelectors": []any{"cd-delegate"}, "connectorRef": "account.shared_connector"}}})
	gw.add("connector", src, memItem{ID: "harnessSecretManager", Raw: map[string]any{"identifier": "harnessSecretManager", "name": "Harness Secrets Manager", "harnessManaged": true, "projectIdentifier": src}})
	yamlDoc := func(noun, id, body string) {
		gw.add(noun, src, memItem{ID: id, Doc: body})
	}
	yamlDoc("service", "svc", "service:\n  identifier: svc\n  name: svc\n  orgIdentifier: "+org+"\n  projectIdentifier: "+src+"\n")
	yamlDoc("environment", "prod", "environment:\n  identifier: prod\n  name: prod\n  orgIdentifier: "+org+"\n  projectIdentifier: "+src+"\n")
	gw.add("infrastructure", src, memItem{ID: "k8s", Parent: "prod", Doc: "infrastructureDefinition:\n  identifier: k8s\n  name: k8s\n  orgIdentifier: " + org + "\n  projectIdentifier: " + src + "\n  environmentRef: prod\n"})
	gw.add("service_override", src, memItem{ID: "ov", Parent: "prod", Raw: map[string]any{"identifier": "ov", "environmentRef": "prod", "serviceRef": "svc", "orgIdentifier": org, "projectIdentifier": src}})
	parentV1 := "template:\n  identifier: parent\n  name: parent\n  versionLabel: v1\n  type: Step\n  orgIdentifier: " + org + "\n  projectIdentifier: " + src + "\n  spec: {}\n"
	parentV2 := "template:\n  identifier: parent\n  name: parent\n  versionLabel: v2\n  type: Step\n  orgIdentifier: " + org + "\n  projectIdentifier: " + src + "\n  spec: {}\n"
	child := "template:\n  identifier: child\n  name: child\n  versionLabel: v1\n  type: Step\n  orgIdentifier: " + org + "\n  projectIdentifier: " + src + "\n  spec:\n    templateRef: parent\n"
	gw.add("template", src, memItem{ID: "parent"})
	gw.add("template_version", src, memItem{ID: "v1", Parent: "parent", Version: "v1", Doc: parentV1})
	gw.add("template_version", src, memItem{ID: "v2", Parent: "parent", Version: "v2", Stable: true, Doc: parentV2})
	gw.add("template", src, memItem{ID: "child"})
	gw.add("template_version", src, memItem{ID: "v1", Parent: "child", Version: "v1", Stable: true, Doc: child})
	pipeline := "pipeline:\n  identifier: deploy\n  name: deploy\n  enabled: true\n  orgIdentifier: " + org + "\n  projectIdentifier: " + src + "\n  connectorRef: org.echo\n  stages:\n    - stage:\n        identifier: child\n        type: Pipeline\n        spec:\n          org: " + org + "\n          project: " + src + "\n          pipeline: childpipe\n          connectorRef: account.shared_connector\n    - stage:\n        identifier: elsewhere\n        type: Pipeline\n        spec:\n          org: " + org + "\n          project: other_project\n          pipeline: foreign\n"
	gw.add("pipeline", src, memItem{ID: "deploy", Doc: pipeline})
	gw.add("pipeline", src, memItem{ID: "gitpipe", Remote: true, Doc: "pipeline:\n  identifier: gitpipe\n  name: git\n  projectIdentifier: " + src + "\n"})
	gw.add("input_set", src, memItem{ID: "inputs", Parent: "deploy", Doc: "inputSet:\n  identifier: inputs\n  name: inputs\n  orgIdentifier: " + org + "\n  projectIdentifier: " + src + "\n  pipeline:\n    identifier: deploy\n"})
	gw.add("trigger", src, memItem{ID: "push", Parent: "deploy", Doc: "trigger:\n  identifier: push\n  name: push\n  enabled: true\n  pipelineIdentifier: deploy\n  orgIdentifier: " + org + "\n  projectIdentifier: " + src + "\n"})
	gw.add("policy", src, memItem{ID: "deny_prod", Raw: map[string]any{"identifier": "deny_prod", "name": "deny", "rego": "package pipeline", "projectIdentifier": src}})
	gw.add("policy_set", src, memItem{ID: "on_run", Raw: map[string]any{"identifier": "on_run", "name": "on run", "type": "pipeline", "action": "onrun", "enabled": true, "policies": []any{map[string]any{"identifier": "deny_prod"}}, "projectIdentifier": src}})
	if conflict {
		gw.add("pipeline", testScope.Destination, memItem{ID: "deploy", Doc: "pipeline:\n  identifier: deploy\n  name: other\n  orgIdentifier: " + org + "\n  projectIdentifier: " + testScope.Destination + "\n"})
	}
	return gw
}
