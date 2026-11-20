// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package projectcopy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestCopyAppliesCDCoreAndSkipsIdenticalRerun(t *testing.T) {
	api := newCopyAPI(false)
	report, err := Copy(context.Background(), api.transport(), testScope, true)
	if err == nil || !strings.Contains(err.Error(), "git-backed") {
		t.Fatalf("expected incomplete copy for skipped git-backed resources: %v\n%s", err, strings.Join(api.seen, "\n"))
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
	if len(report.DelegateSelectors) != 1 || !strings.Contains(report.DelegateSelectors[0].Reason, "harness-delegate") {
		t.Fatalf("delegate check %#v", report.DelegateSelectors)
	}
	var secretBody, triggerBody, projectBody string
	for _, call := range api.mutating {
		switch {
		case strings.Contains(call.body, "db_password"):
			secretBody = call.body
		case strings.Contains(call.path, "/triggers"):
			triggerBody = call.body
		case call.path == "/ng/api/projects":
			projectBody = call.body
		}
	}
	if !strings.Contains(secretBody, InlineSecretPlaceholder) || strings.Contains(secretBody, "SUPER_SECRET") {
		t.Fatalf("inline secret body = %s", secretBody)
	}
	if !strings.Contains(triggerBody, "enabled: false") {
		t.Fatalf("trigger body = %s", triggerBody)
	}
	if !strings.Contains(projectBody, `"identifier":"destination"`) || !strings.Contains(projectBody, `"name":"destination"`) || strings.Contains(projectBody, `"name":"Source"`) || !strings.Contains(projectBody, "CD") {
		t.Fatalf("project body = %s", projectBody)
	}
	for _, call := range api.mutating {
		if strings.Contains(call.path, "delegate") {
			t.Fatal("delegate was created")
		}
	}
	before := len(api.mutating)
	again, err := Copy(context.Background(), api.transport(), testScope, true)
	if err == nil || !strings.Contains(err.Error(), "git-backed") {
		t.Fatalf("expected incomplete rerun for skipped git-backed resources: %v\n%s", err, strings.Join(api.seen, "\n"))
	}
	if len(api.mutating) != before {
		t.Fatalf("rerun wrote %d extra resources", len(api.mutating)-before)
	}
	if !containsResult(again, "secret", "db_password", ActionSkip) || !containsResult(again, "pipeline", "deploy", ActionSkip) {
		t.Fatal("identical rerun did not skip")
	}
}

func TestCopyPlanDoesNotWrite(t *testing.T) {
	api := newCopyAPI(false)
	report, err := Copy(context.Background(), api.transport(), testScope, false)
	if err == nil || !strings.Contains(err.Error(), "git-backed") {
		t.Fatalf("expected incomplete plan for skipped git-backed resources: %v", err)
	}
	if len(api.mutating) != 0 {
		t.Fatalf("plan wrote %#v", api.mutating)
	}
	if !containsResult(report, "secret", "db_password", ActionCreate) || !strings.Contains(strings.Join(report.SecretsPendingValue, ","), "db_password") {
		t.Fatal("plan did not include the inline secret")
	}
}

func TestCopyConflictWritesNothing(t *testing.T) {
	api := newCopyAPI(true)
	report, err := Copy(context.Background(), api.transport(), testScope, true)
	if err == nil || !strings.Contains(err.Error(), "nothing written") && !strings.Contains(err.Error(), "differs") {
		t.Fatalf("err=%v", err)
	}
	if len(api.mutating) != 0 {
		t.Fatalf("conflict wrote %#v", api.mutating)
	}
	if !containsResult(report, "pipeline", "deploy", ActionConflict) {
		t.Fatal("conflict missing from report")
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

func TestCDEnvelopeWrapsServiceYAML(t *testing.T) {
	env, err := cdEnvelope("service", "service:\n  identifier: demo_app\n  name: demo-app\n  orgIdentifier: fixture_org\n  projectIdentifier: destination\n")
	if err != nil {
		t.Fatal(err)
	}
	if env["identifier"] != "demo_app" || env["projectIdentifier"] != "destination" {
		t.Fatalf("%v", env)
	}
	yamlField, _ := env["yaml"].(string)
	if !strings.Contains(yamlField, "identifier: demo_app") {
		t.Fatal(yamlField)
	}
}

func TestRewriteDisablesTriggersAndRewritesProject(t *testing.T) {
	doc, err := rewriteYAML("trigger:\n  identifier: push\n  enabled: true\n  pipelineIdentifier: deploy\n  projectIdentifier: source\n", rewriteOpts{Org: testScope.Org, Project: testScope.Destination, SourceOrg: testScope.Org, SourceProject: testScope.Source, DisableTriggers: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc, "projectIdentifier: "+testScope.Destination) || !strings.Contains(doc, "enabled: false") || strings.Contains(doc, "projectIdentifier: source") {
		t.Fatal(doc)
	}
}

func TestRewritePreservesCrossProjectRefs(t *testing.T) {
	doc, err := rewriteYAML("pipeline:\n  projectIdentifier: source\n  stages:\n    - stage:\n        spec:\n          pipeline:\n            projectIdentifier: other_project\n            orgIdentifier: other_org\n", rewriteOpts{Org: testScope.Org, Project: testScope.Destination, SourceOrg: testScope.Org, SourceProject: testScope.Source})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc, "projectIdentifier: "+testScope.Destination) || !strings.Contains(doc, "projectIdentifier: other_project") || !strings.Contains(doc, "orgIdentifier: other_org") {
		t.Fatal(doc)
	}
}

func TestListFailsClosedOnLaterPageError(t *testing.T) {
	calls := 0
	tr := fakeTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return response(200, `{"status":"SUCCESS","data":{"content":[{"identifier":"a"}],"totalItems":2,"totalPages":2}}`), nil
		}
		return response(404, `{"status":"ERROR","code":"RESOURCE_NOT_FOUND"}`), nil
	})
	c := &copier{ctx: context.Background(), transport: tr, scope: testScope}
	if _, err := c.list(http.MethodGet, "/items", url.Values{}, "", "page", "size", ""); err == nil {
		t.Fatal("later page 404 accepted as complete")
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
		t.Fatalf("%v", ordered)
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

func TestMutatingPostIsNotRetried(t *testing.T) {
	calls := 0
	tr := fakeTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return response(503, `{"status":"ERROR"}`), nil
	})
	c := &copier{ctx: context.Background(), transport: tr, scope: testScope}
	if err := c.postJSON("/ng/api/connectors", c.query(testScope.Destination), `{"connector":{}}`); err == nil {
		t.Fatal("mutating 503 accepted")
	}
	if calls != 1 {
		t.Fatalf("mutating POST retried %d times", calls)
	}
	calls = 0
	status, _, err := tr.exchange(context.Background(), http.MethodGet, "/ng/api/projects/destination", c.query(testScope.Destination), "", "")
	if err != nil || status != 503 {
		t.Fatalf("status=%d err=%v", status, err)
	}
	if calls != 3 {
		t.Fatalf("GET 503 retried %d times", calls)
	}
}

func TestCopyResumesAfterCreateFailure(t *testing.T) {
	api := newCopyAPI(false)
	api.failPath = "/ng/api/servicesV2"
	api.failOnce = true
	report, err := Copy(context.Background(), api.transport(), testScope, true)
	if err == nil || report.Readiness != "copy stopped after a create failure; re-run to continue" {
		t.Fatalf("err=%v readiness=%q", err, report.Readiness)
	}
	if containsResult(report, "service", "svc", ActionCreate) {
		t.Fatal("failed create recorded as success")
	}
	projectWrites := 0
	for _, call := range api.mutating {
		if call.path == "/ng/api/projects" {
			projectWrites++
		}
		if call.path == "/ng/api/servicesV2" {
			t.Fatal("failed service create was stored")
		}
	}
	if projectWrites != 1 {
		t.Fatalf("project writes = %d", projectWrites)
	}
	again, err := Copy(context.Background(), api.transport(), testScope, true)
	if err == nil || !strings.Contains(err.Error(), "git-backed") {
		t.Fatalf("expected resume to finish remaining writes: %v", err)
	}
	if !containsResult(again, "project", testScope.Destination, ActionSkip) || !containsResult(again, "service", "svc", ActionCreate) {
		t.Fatalf("resume results %#v", again.Results)
	}
	projectWrites = 0
	for _, call := range api.mutating {
		if call.path == "/ng/api/projects" {
			projectWrites++
		}
	}
	if projectWrites != 1 {
		t.Fatalf("resume recreated the project: %d writes", projectWrites)
	}
}

func TestCopyStableTemplateFailureStopsCopy(t *testing.T) {
	api := newCopyAPI(false)
	api.rejectStable = true
	report, err := Copy(context.Background(), api.transport(), testScope, true)
	if err == nil || report.Readiness != "copy stopped after a create failure; re-run to continue" {
		t.Fatalf("err=%v readiness=%q", err, report.Readiness)
	}
	if !containsResult(report, "template", "step/v1", ActionBlocked) {
		t.Fatalf("stable-template failure %#v", report.Results)
	}
}

func TestExtractItemsRejectsUnprovenLists(t *testing.T) {
	for _, body := range []string{`{}`, `{"status":"SUCCESS"}`, `{"status":"SUCCESS","data":null}`, `{"status":"ERROR","data":{"content":[]}}`} {
		if _, err := extractItems([]byte(body)); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
	page, err := extractItems([]byte(`{"status":"SUCCESS","data":{"content":[],"totalItems":0,"totalPages":0}}`))
	if err != nil || len(page.Items) != 0 || !page.HasTotalItems {
		t.Fatalf("%v %#v", err, page)
	}
}

func containsResult(report Report, kind, id string, action Action) bool {
	for _, item := range report.Results {
		if item.Kind == kind && item.Identifier == id && item.Action == action {
			return true
		}
	}
	return false
}

type copyCall struct{ method, path, body, project string }

type copyAPI struct {
	conflict     bool
	failOnce     bool
	failPath     string
	rejectStable bool
	mutating     []copyCall
	stored       map[string]string
	seen         []string
}

func newCopyAPI(conflict bool) *copyAPI {
	return &copyAPI{conflict: conflict, stored: map[string]string{}}
}

func (a *copyAPI) transport() *Transport {
	return fakeTransport(func(r *http.Request) (*http.Response, error) {
		var body []byte
		if r.Body != nil {
			body, _ = io.ReadAll(r.Body)
		}
		path := strings.TrimPrefix(r.URL.Path, "/gateway")
		project := r.URL.Query().Get("projectIdentifier")
		a.seen = append(a.seen, r.Method+" "+path+" project="+project)
		if r.Method == http.MethodPost || r.Method == http.MethodPut {
			if !strings.Contains(path, "/list") {
				if a.failOnce && r.Method == http.MethodPost && path == a.failPath {
					a.failOnce = false
					return response(500, `{"status":"ERROR","code":"UNKNOWN"}`), nil
				}
				if a.rejectStable && r.Method == http.MethodPut && strings.Contains(path, "updateStableTemplate") {
					return response(400, `{"status":"ERROR","code":"INVALID_REQUEST"}`), nil
				}
				call := copyCall{method: r.Method, path: path, body: string(body), project: project}
				a.mutating = append(a.mutating, call)
				a.stored[storeKey(call)] = string(body)
			}
		}
		return response(a.serve(r.Method, path, project, string(body))), nil
	})
}

func (a *copyAPI) serve(method, path, project, body string) (int, string) {
	switch {
	case path == "/ng/api/v2/secrets/list/secrets":
		if project != testScope.Source {
			return 200, emptySecretPage()
		}
		return 200, sourceSecrets()
	case strings.HasPrefix(path, "/ng/api/v2/secrets/") && method == http.MethodGet:
		return a.storedSecret(project, strings.TrimPrefix(path, "/ng/api/v2/secrets/"))
	case path == "/ng/api/projects/"+testScope.Source:
		return 200, `{"status":"SUCCESS","data":{"project":{"identifier":"source","name":"Source","orgIdentifier":"fixture_org","description":"demo","color":"#004fc4","modules":["CD"]}}}`
	case path == "/ng/api/projects/"+testScope.Destination && method == http.MethodGet:
		if _, ok := a.stored[http.MethodPost+" "+"/ng/api/projects"+" "+""]; ok || a.conflict {
			return 200, `{"status":"SUCCESS","data":{"project":{"identifier":"destination","name":"Source","orgIdentifier":"fixture_org"}}}`
		}
		return 404, `{"status":"ERROR","code":"RESOURCE_NOT_FOUND"}`
	case path == "/ng/api/connectors/listV2":
		if project != testScope.Source {
			return 200, emptyPage()
		}
		return 200, `{"status":"SUCCESS","data":{"content":[{"connector":{"identifier":"github","name":"github","type":"Github","orgIdentifier":"fixture_org","projectIdentifier":"source","spec":{"delegateSelectors":["cd-delegate"],"connectorRef":"account.shared_connector"}}}],"totalPages":1,"totalItems":1}}`
	case strings.HasPrefix(path, "/ng/api/connectors/") && method == http.MethodGet:
		return a.storedJSON(http.MethodPost+" /ng/api/connectors "+project, "connector")
	case path == "/ng/api/servicesV2" && method == http.MethodGet:
		return a.yamlList(project, "service", "svc", "service:\n  identifier: svc\n  name: svc\n  orgIdentifier: fixture_org\n  projectIdentifier: source\n")
	case strings.HasPrefix(path, "/ng/api/servicesV2/") && method == http.MethodGet:
		return a.storedYAML(http.MethodPost+" /ng/api/servicesV2 "+project, "service")
	case path == "/ng/api/environmentsV2" && method == http.MethodGet:
		return a.yamlList(project, "environment", "prod", "environment:\n  identifier: prod\n  name: prod\n  orgIdentifier: fixture_org\n  projectIdentifier: source\n")
	case strings.HasPrefix(path, "/ng/api/environmentsV2/") && path != "/ng/api/environmentsV2/serviceOverrides" && method == http.MethodGet:
		return a.storedYAML(http.MethodPost+" /ng/api/environmentsV2 "+project, "environment")
	case path == "/ng/api/infrastructures" || path == "/ng/api/environmentsV2/serviceOverrides":
		return 200, emptyPage()
	case path == "/template/api/templates/list":
		if project != testScope.Source {
			return 200, emptyPage()
		}
		return 200, `{"status":"SUCCESS","data":{"content":[{"identifier":"step","name":"step","versionLabel":"v1","stableTemplate":true,"storeType":"INLINE"}],"totalPages":1,"totalItems":1}}`
	case strings.HasPrefix(path, "/template/api/templates/") && method == http.MethodGet:
		return a.template(project)
	case path == "/pipeline/api/pipelines/list":
		if project != testScope.Source {
			return 200, emptyPage()
		}
		return 200, `{"status":"SUCCESS","data":{"content":[{"identifier":"deploy","name":"deploy","storeType":"INLINE"},{"identifier":"gitpipe","name":"gitpipe","storeType":"REMOTE"}],"totalPages":1,"totalItems":2}}`
	case path == "/pipeline/api/pipelines/deploy" && method == http.MethodGet:
		if project == testScope.Destination && a.conflict {
			return 200, `{"status":"SUCCESS","data":{"yamlPipeline":"pipeline:\n  identifier: deploy\n  name: other\n  orgIdentifier: fixture_org\n  projectIdentifier: destination\n"}}`
		}
		if project == testScope.Destination {
			if doc, ok := a.stored[http.MethodPost+" /pipeline/api/pipelines/v2 "+project]; ok {
				return 200, `{"status":"SUCCESS","data":{"yamlPipeline":` + jsonString(doc) + `}}`
			}
			return 404, `{"status":"ERROR","code":"RESOURCE_NOT_FOUND"}`
		}
		return 200, `{"status":"SUCCESS","data":{"yamlPipeline":"pipeline:\n  identifier: deploy\n  name: deploy\n  orgIdentifier: fixture_org\n  projectIdentifier: source\n"}}`
	case path == "/pipeline/api/inputSets" && method == http.MethodGet:
		return a.docList(project, "inputSet:\n  identifier: inputs\n  name: inputs\n  orgIdentifier: fixture_org\n  projectIdentifier: source\n  pipeline:\n    identifier: deploy\n", "inputs")
	case path == "/pipeline/api/inputSets/inputs" && method == http.MethodGet:
		return a.storedDoc(http.MethodPost+" /pipeline/api/inputSets "+project, "inputSetYaml")
	case path == "/pipeline/api/triggers" && method == http.MethodGet:
		return a.docList(project, "trigger:\n  identifier: push\n  name: push\n  enabled: true\n  pipelineIdentifier: deploy\n  orgIdentifier: fixture_org\n  projectIdentifier: source\n", "push")
	case path == "/pipeline/api/triggers/push" && method == http.MethodGet:
		return a.storedDoc(http.MethodPost+" /pipeline/api/triggers "+project, "yaml")
	case path == "/pm/api/v1/policies" && method == http.MethodGet:
		if project != testScope.Source {
			return 200, emptyPage()
		}
		return 200, `{"status":"SUCCESS","data":{"content":[{"identifier":"deny_prod","name":"deny","rego":"package pipeline","projectIdentifier":"source"}],"totalPages":1,"totalItems":1}}`
	case path == "/pm/api/v1/policies/deny_prod" && method == http.MethodGet:
		return a.storedGovernance(http.MethodPost + " /pm/api/v1/policies " + project)
	case path == "/pm/api/v1/policysets" && method == http.MethodGet:
		if project != testScope.Source {
			return 200, emptyPage()
		}
		return 200, `{"status":"SUCCESS","data":{"content":[{"identifier":"on_run","name":"on run","type":"pipeline","action":"onrun","enabled":true,"policies":[{"identifier":"deny_prod"}],"projectIdentifier":"source"}],"totalPages":1,"totalItems":1}}`
	case path == "/pm/api/v1/policysets/on_run" && method == http.MethodGet:
		return a.storedGovernance(http.MethodPost + " /pm/api/v1/policysets " + project)
	case path == "/ng/api/delegate-setup/listDelegates":
		return 200, `{"status":"SUCCESS","resource":[{"groupName":"harness-delegate","delegateGroupIdentifier":"del1","connectivityStatus":"connected","tags":["cd-delegate"]}],"totalPages":1}`
	case method == http.MethodPost || method == http.MethodPut:
		return 200, `{"status":"SUCCESS"}`
	default:
		return 404, `{"status":"ERROR","code":"RESOURCE_NOT_FOUND"}`
	}
}

func (a *copyAPI) storedSecret(project, id string) (int, string) {
	if project == testScope.Source && id == "db_password" {
		return 200, `{"status":"SUCCESS","data":{"secret":{"identifier":"db_password","name":"db","type":"SecretText","orgIdentifier":"fixture_org","projectIdentifier":"source","spec":{"type":"SecretText","valueType":"Inline","secretManagerIdentifier":"harnessSecretManager","value":"SUPER_SECRET"}},"updatedAt":17}}`
	}
	if project == testScope.Source && id == "ref_token" {
		return 200, `{"status":"SUCCESS","data":{"secret":{"identifier":"ref_token","name":"ref","type":"SecretText","orgIdentifier":"fixture_org","projectIdentifier":"source","spec":{"type":"SecretText","valueType":"Reference","secretManagerIdentifier":"org.manager","value":"vault/path#key"}},"updatedAt":17}}`
	}
	raw, ok := a.stored[http.MethodPost+" /ng/api/v2/secrets "+project+" "+id]
	if !ok {
		return 404, `{"status":"ERROR","code":"RESOURCE_NOT_FOUND"}`
	}
	var body map[string]any
	if json.Unmarshal([]byte(raw), &body) != nil {
		return 404, `{"status":"ERROR","code":"RESOURCE_NOT_FOUND"}`
	}
	data, _ := json.Marshal(map[string]any{"status": "SUCCESS", "data": body})
	return 200, string(data)
}

func sourceSecrets() string {
	return `{"status":"SUCCESS","data":{"totalPages":1,"totalItems":2,"pageItemCount":2,"pageSize":100,"content":[` +
		`{"secret":{"identifier":"db_password","name":"db","orgIdentifier":"fixture_org","projectIdentifier":"source","type":"SecretText","spec":{"type":"SecretText","valueType":"Inline","secretManagerIdentifier":"harnessSecretManager","value":"SUPER_SECRET"}},"updatedAt":17},` +
		`{"secret":{"identifier":"ref_token","name":"ref","orgIdentifier":"fixture_org","projectIdentifier":"source","type":"SecretText","spec":{"type":"SecretText","valueType":"Reference","secretManagerIdentifier":"org.manager","value":"vault/path#key"}},"updatedAt":18}` +
		`]}}`
}

func (a *copyAPI) yamlList(project, root, id, doc string) (int, string) {
	if project != testScope.Source {
		return 200, emptyPage()
	}
	return 200, `{"status":"SUCCESS","data":{"content":[{"` + root + `":{"identifier":"` + id + `","name":"` + id + `","orgIdentifier":"fixture_org","projectIdentifier":"source","yaml":` + jsonString(doc) + `}}],"totalPages":1,"totalItems":1}}`
}

func (a *copyAPI) storedYAML(key, root string) (int, string) {
	doc, ok := a.stored[key]
	if !ok {
		return 404, `{"status":"ERROR","code":"RESOURCE_NOT_FOUND"}`
	}
	var env map[string]any
	if json.Unmarshal([]byte(doc), &env) == nil {
		if inner, ok := env["yaml"].(string); ok {
			doc = inner
		}
	}
	return 200, `{"status":"SUCCESS","data":{"` + root + `":{"yaml":` + jsonString(doc) + `}}}`
}

func (a *copyAPI) storedJSON(key, root string) (int, string) {
	raw, ok := a.stored[key]
	if !ok {
		return 404, `{"status":"ERROR","code":"RESOURCE_NOT_FOUND"}`
	}
	var body map[string]any
	if json.Unmarshal([]byte(raw), &body) != nil {
		return 404, `{"status":"ERROR","code":"RESOURCE_NOT_FOUND"}`
	}
	entity, _ := body[root]
	data, _ := json.Marshal(map[string]any{"status": "SUCCESS", "data": map[string]any{root: entity}})
	return 200, string(data)
}

func (a *copyAPI) template(project string) (int, string) {
	if project == testScope.Source {
		return 200, `{"status":"SUCCESS","data":{"yaml":` + jsonString("template:\n  identifier: step\n  name: step\n  versionLabel: v1\n  type: Step\n  orgIdentifier: fixture_org\n  projectIdentifier: source\n  spec: {}\n") + `}}`
	}
	if doc, ok := a.stored[http.MethodPost+" /template/api/templates "+project]; ok {
		return 200, `{"status":"SUCCESS","data":{"yaml":` + jsonString(doc) + `}}`
	}
	return 404, `{"status":"ERROR","code":"RESOURCE_NOT_FOUND"}`
}

func (a *copyAPI) docList(project, doc, id string) (int, string) {
	if project != testScope.Source {
		return 200, emptyPage()
	}
	return 200, `{"status":"SUCCESS","data":{"content":[{"identifier":"` + id + `","name":"` + id + `","storeType":"INLINE","yaml":` + jsonString(doc) + `,"inputSetYaml":` + jsonString(doc) + `}],"totalPages":1,"totalItems":1}}`
}

func (a *copyAPI) storedDoc(key, field string) (int, string) {
	doc, ok := a.stored[key]
	if !ok {
		return 404, `{"status":"ERROR","code":"RESOURCE_NOT_FOUND"}`
	}
	data, _ := json.Marshal(map[string]any{"status": "SUCCESS", "data": map[string]any{field: doc, "yaml": doc}})
	return 200, string(data)
}

func (a *copyAPI) storedGovernance(key string) (int, string) {
	raw, ok := a.stored[key]
	if !ok {
		return 404, `{"status":"ERROR","code":"RESOURCE_NOT_FOUND"}`
	}
	return 200, `{"status":"SUCCESS","data":` + raw + `}`
}

func emptyPage() string {
	return `{"status":"SUCCESS","data":{"content":[],"totalPages":0,"totalItems":0}}`
}

func emptySecretPage() string {
	return `{"status":"SUCCESS","data":{"totalPages":0,"totalItems":0,"pageItemCount":0,"pageSize":100,"content":[]}}`
}

func storeKey(call copyCall) string {
	key := call.method + " " + call.path + " " + call.project
	if call.path == "/ng/api/v2/secrets" {
		var body map[string]any
		if json.Unmarshal([]byte(call.body), &body) == nil {
			if secret, ok := body["secret"].(map[string]any); ok {
				if id, ok := secret["identifier"].(string); ok {
					return key + " " + id
				}
			}
		}
	}
	return key
}

func jsonString(s string) string {
	data, _ := json.Marshal(s)
	return string(data)
}
