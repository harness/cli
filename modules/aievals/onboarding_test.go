// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/harness/cli/v3/pkg/auth"
	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/hbase"
	"github.com/harness/cli/v3/pkg/registry"
	"github.com/harness/cli/v3/pkg/specloader"
)

func TestSkillEmbeddedVersionAndRules(t *testing.T) {
	text := SkillText()
	if !strings.Contains(text, "version: 1") || !strings.Contains(text, SchemaVersion) {
		t.Fatalf("skill is missing its version handshake:\n%s", text)
	}
	if !strings.Contains(text, "name: aievals") {
		t.Fatal("skill frontmatter is not the general AI Evals skill")
	}
	for _, rule := range SkillRules() {
		if !strings.Contains(text, rule) {
			t.Errorf("skill drifted from rule %q", rule)
		}
	}
	for _, question := range []string{
		"What local URL should the eval call?",
		"Which HTTP method should the eval use?",
		"Where does the request text go in the body?",
		"Where is the reply text in the response?",
		"Is an auth header name required? Reply with the header name, or none. Do not send a secret value.",
		"Checkpoint: Use this endpoint, pick another candidate, or set one up?",
		"## Generic local-agent prerequisite check",
		"Ask exactly one prerequisite question at a time",
		"Checkpoint: Are the local service, environment-variable names, optional auth header mapping, and external LLM prerequisite ready?",
		"Checkpoint: Which metrics and thresholds should we use, and are you comfortable with any listed provider requirements?",
		"Checkpoint: Write these files?",
		"Checkpoint: Run this eval locally?",
		"Checkpoint: What should we do next: run against a live agent, upload to Harness, run on the platform, view results, or add cases or metrics?",
		"Checkpoint: Run this eval against that live environment?",
		"Checkpoint: Upload this eval to Harness?",
		"Checkpoint: Run it on the platform?",
		"Checkpoint: Open the result links?",
		"Checkpoint: Write these updated files?",
	} {
		if !strings.Contains(text, question) {
			t.Errorf("skill missing checkpoint %q", question)
		}
	}
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "do not") || strings.HasPrefix(lower, "never") {
			continue
		}
		if strings.Contains(lower, "eval.yaml") || strings.Contains(lower, "standalone python runner") || strings.Contains(lower, "write a python") {
			t.Errorf("skill instructs a runner script or eval.yaml: %s", trimmed)
		}
	}
	for _, required := range []string{
		"harness setup eval_onboarding",
		"harness scan eval_onboarding",
		"harness scan eval_onboarding --metrics",
		"options_schema",
		"Never omit `metrics` in this workflow",
		"harness stage eval_onboarding",
		"harness run eval_onboarding",
		"harness upload eval_onboarding",
		"--profile <name>",
		"HARNESS_PROFILE",
		"The stable onboarding interface",
		"Do not invent MCP tool names.",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("skill missing %q", required)
		}
	}
}

func TestSkillModuleRecordsSourceAndHash(t *testing.T) {
	rendered := FormatSkillModule(SkillSourcePath, SkillText())
	if !strings.Contains(rendered, "source: "+SkillSourcePath) {
		t.Fatalf("generated module is missing the source path:\n%s", rendered)
	}
	sum := sha256.Sum256([]byte(SkillText()))
	want := hex.EncodeToString(sum[:])
	if !strings.Contains(rendered, "sha256: "+want) || !strings.Contains(rendered, `AI_EVALS_SKILL_SHA256 = "`+want+`"`) {
		t.Fatalf("generated module hash drifted from the skill bytes")
	}
	if !strings.Contains(rendered, "Do not edit by hand.") {
		t.Fatal("generated module is missing the hand-edit warning")
	}
}

func TestPlaceSkillExplicitDetectedOrStdout(t *testing.T) {
	repo := t.TempDir()
	explicit := filepath.Join(t.TempDir(), "skills")
	dest, err := PlaceSkill(repo, explicit, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != SkillText() {
		t.Fatal("explicit skill bytes differ from the embedded asset")
	}

	cursorRepo := t.TempDir()
	if err := os.Mkdir(filepath.Join(cursorRepo, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	dest, err = PlaceSkill(cursorRepo, "", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dest, filepath.Join(".cursor", "skills", "aievals", "SKILL.md")) {
		t.Fatalf("detected dest = %s", dest)
	}

	var stdout bytes.Buffer
	dest, err = PlaceSkill(t.TempDir(), "", &stdout)
	if err != nil {
		t.Fatal(err)
	}
	if dest != "" || stdout.String() != SkillText() {
		t.Fatalf("stdout fallback dest=%q bytes=%d", dest, stdout.Len())
	}
}

func TestInferenceFixtures(t *testing.T) {
	java := inferFixture(t, "java-support")
	if java.Language != "java" {
		t.Fatalf("language %s", java.Language)
	}
	requireHigh(t, java, "endpoint_path", "/api/chat")
	requireHigh(t, java, "method", "POST")
	requireHigh(t, java, "port", "8080")
	requireHigh(t, java, "response_path", "output.text")
	requireHigh(t, java, "auth_header", "Authorization")
	requireHigh(t, java, "auth_env", "SUPPORT_TOKEN")
	requireHigh(t, java, "test_input_text", "hello")
	requireHigh(t, java, "test_expected", "ok")
	assertTree(t, java, "support", "8080", "/api/chat", true)
	if ApplyFiles(java, false, nil) == nil {
		t.Fatal("spring fixture did not stage")
	}

	ts := inferFixture(t, "typescript-support")
	if ts.Language != "typescript" {
		t.Fatalf("language %s", ts.Language)
	}
	requireHigh(t, ts, "endpoint_path", "/chat")
	requireHigh(t, ts, "port", "3000")
	requireHigh(t, ts, "response_path", "output.text")
	requireHigh(t, ts, "auth_env", "SUPPORT_TOKEN")
	assertTree(t, ts, "support", "3000", "/chat", true)
	if ApplyFiles(ts, false, nil) == nil {
		t.Fatal("typescript fixture did not stage")
	}

	py := inferFixture(t, "python-support")
	if py.Language != "python" {
		t.Fatalf("language %s", py.Language)
	}
	requireHigh(t, py, "endpoint_path", "/chat")
	requireHigh(t, py, "port", "8000")
	requireHigh(t, py, "response_path", "output.text")
	requireHigh(t, py, "auth_env", "SUPPORT_TOKEN")
	assertTree(t, py, "support", "8000", "/chat", true)
	if ApplyFiles(py, false, nil) == nil {
		t.Fatal("python fixture did not stage")
	}
}

func TestRendererOmitsSecretHeaderWithoutReference(t *testing.T) {
	inf := Inference{
		AgentID:   "analysis",
		HasSignal: true,
		Evidence: []FieldEvidence{
			{Field: "agent_id", Value: "analysis", Confidence: ConfidenceHigh, Source: "test"},
			{Field: "endpoint_path", Value: "/analyze", Confidence: ConfidenceHigh, Source: "test"},
			{Field: "method", Value: "POST", Confidence: ConfidenceHigh, Source: "test"},
			{Field: "port", Value: "8080", Confidence: ConfidenceHigh, Source: "test"},
			{Field: "response_path", Value: "output.text", Confidence: ConfidenceHigh, Source: "test"},
			{Field: "auth_header", Value: "Authorization", Confidence: ConfidenceHigh, Source: "test"},
		},
	}
	files := ApplyFiles(inf, true, nil)
	agentYAML := files["agents/analysis/agent.yaml"]
	if strings.Contains(agentYAML, "harness_secret") || strings.Contains(agentYAML, "Authorization:") {
		t.Fatalf("header without an evidenced secret reference was staged:\n%s", agentYAML)
	}
}

func TestRendererKeepsDatasetRowsValidJSONLWhenExpectedIsUnknown(t *testing.T) {
	inf := Inference{
		AgentID:   "analysis",
		HasSignal: true,
		Evidence: []FieldEvidence{
			{Field: "agent_id", Value: "analysis", Confidence: ConfidenceHigh, Source: "test"},
			{Field: "endpoint_path", Value: "/analyze", Confidence: ConfidenceHigh, Source: "test"},
			{Field: "method", Value: "POST", Confidence: ConfidenceHigh, Source: "test"},
			{Field: "port", Value: "8080", Confidence: ConfidenceHigh, Source: "test"},
			{Field: "response_path", Value: "output.text", Confidence: ConfidenceHigh, Source: "test"},
			{Field: "expected_gap", Value: "expected value was not evidenced", Confidence: ConfidenceHigh, Source: "test"},
		},
	}
	dataset := ApplyFiles(inf, true, nil)["datasets/analysis.jsonl"]
	for _, line := range strings.Split(strings.TrimSpace(dataset), "\n") {
		if !json.Valid([]byte(line)) {
			t.Fatalf("dataset row is not valid JSON: %q", line)
		}
	}
}

func TestRendererQuotesNumericAgentIdentifiers(t *testing.T) {
	inf := Inference{
		AgentID:   "001",
		HasSignal: true,
		Evidence: []FieldEvidence{
			{Field: "agent_id", Value: "001", Confidence: ConfidenceHigh, Source: "test"},
			{Field: "endpoint_path", Value: "/analyze", Confidence: ConfidenceHigh, Source: "test"},
			{Field: "method", Value: "POST", Confidence: ConfidenceHigh, Source: "test"},
			{Field: "port", Value: "8080", Confidence: ConfidenceHigh, Source: "test"},
			{Field: "response_path", Value: "output.text", Confidence: ConfidenceHigh, Source: "test"},
		},
	}
	agentYAML := ApplyFiles(inf, true, nil)["agents/001/agent.yaml"]
	if !strings.Contains(agentYAML, "identifier: \"001\"") {
		t.Fatalf("numeric identifier was not rendered as a YAML string:\n%s", agentYAML)
	}
}

func TestJavaRecordRouteKeepsDTOsParamsAndAuthLocal(t *testing.T) {
	inf := inferFixture(t, "java-rca-records")
	requireHigh(t, inf, "endpoint_path", "/api/v1/mc/evals/rca/analyze")
	requireHigh(t, inf, "method", "POST")
	requireHigh(t, inf, "port", "8080")
	requireHigh(t, inf, "request_fields", "mode,query,scope,context,toolResults")
	requireHigh(t, inf, "response_path", "analysis")
	requireHigh(t, inf, "required_query_params", "accountId,orgId,projectId")
	if _, ok := inf.ByField("auth_header"); ok {
		t.Fatalf("@HarnessAuth without explicit header configuration must not invent a header: %+v", inf.Evidence)
	}
	if !strings.Contains(strings.Join(inf.Notes, "\n"), "@HarnessAuth") {
		t.Fatalf("missing safe auth configuration guidance: %v", inf.Notes)
	}
}

func TestJaxRSConformanceFixturePresentsTypedEvidence(t *testing.T) {
	fixture := filepath.Join("testdata", "java-jaxrs-conformance")
	inf := inferFixture(t, "java-jaxrs-conformance")
	requireHigh(t, inf, "endpoint_path", "/api/v1/analysis/analyze")
	requireHigh(t, inf, "method", "POST")
	requireHigh(t, inf, "request_fields", "mode,query,validServices,validEnvironments,impactedServices,incidentContext,baseTime,currentTheories,includeChanges,includeAlerts,includeFlags,toolResults")
	requireHigh(t, inf, "response_path", "reasoning")
	requireHigh(t, inf, "required_query_params", "accountId,orgId,projectId")
	if _, ok := inf.ByField("auth_header"); ok {
		t.Fatalf("@HarnessAuth must not invent a header mapping: %+v", inf.Evidence)
	}
	if !strings.Contains(strings.Join(inf.Notes, "\n"), "@HarnessAuth") {
		t.Fatalf("missing route-local auth evidence: %v", inf.Notes)
	}

	report, code, err := Scan(fixture, "POST /api/v1/analysis/analyze")
	if err != nil || code != 0 || len(report.Candidates) != 1 {
		t.Fatalf("code=%d err=%v candidates=%d", code, err, len(report.Candidates))
	}
	candidate := report.Candidates[0]
	if candidate.Target.ResponsePath != "reasoning" {
		t.Fatalf("response path = %q", candidate.Target.ResponsePath)
	}
	for _, field := range strings.Split("mode,query,validServices,validEnvironments,impactedServices,incidentContext,baseTime,currentTheories,includeChanges,includeAlerts,includeFlags,toolResults", ",") {
		if _, ok := candidate.Target.RequestTemplate[field]; !ok {
			t.Fatalf("request template omitted typed field %q: %#v", field, candidate.Target.RequestTemplate)
		}
	}
	if len(candidate.Target.AuthHeaders) != 0 {
		t.Fatalf("auth mapping was invented: %#v", candidate.Target.AuthHeaders)
	}
	summary := candidate.Evidence[0].Summary
	for _, evidence := range []string{
		"POST /api/v1/analysis/analyze",
		"required query parameters accountId,orgId,projectId",
		"@HarnessAuth detected",
	} {
		if !strings.Contains(summary, evidence) {
			t.Fatalf("candidate summary omitted %q: %q", evidence, summary)
		}
	}
}

func TestJavaOperationalRoutesDoNotCauseAmbiguity(t *testing.T) {
	inf := inferFixture(t, "java-multi-confidence")
	if err := inf.RouteAmbiguity(); err != nil {
		t.Fatalf("operational route caused ambiguity: %v", err)
	}
	requireHigh(t, inf, "endpoint_path", "/api/work/analyze")
	for _, candidate := range inf.RouteCandidates {
		if candidate.Route() == "GET /api/work/health" {
			t.Fatalf("operational route was retained: %+v", candidate)
		}
	}
}

func TestNonJavaSamePathDifferentMethodsRequireSelection(t *testing.T) {
	inf := inferFixture(t, "python-multi-route")
	err := inf.RouteAmbiguity()
	if err == nil {
		t.Fatalf("same path with different methods must remain distinct candidates: %+v", inf)
	}
	for _, candidate := range []string{"GET /reports", "POST /reports"} {
		if !strings.Contains(err.Error(), candidate) {
			t.Fatalf("candidate %q missing from ambiguity error: %v", candidate, err)
		}
	}
}

func TestNonJavaAmbiguityRefusesStaging(t *testing.T) {
	repo := copyFixture(t, "python-multi-route")
	report, code, err := Scan(repo, "")
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	if !strings.Contains(text, "GET /reports") || !strings.Contains(text, "POST /reports") {
		t.Fatalf("scan JSON = %s", text)
	}
	if len(report.Candidates) < 2 {
		t.Fatalf("candidates = %+v", report.Candidates)
	}
	assertNoEvalFiles(t, repo)
}

func TestNonJavaRouteSelectionDoesNotLeakProtectedEvidence(t *testing.T) {
	for _, fixture := range []string{"python-route-local", "typescript-route-local"} {
		t.Run(fixture, func(t *testing.T) {
			inf, err := InferWithRoute(filepath.Join("testdata", fixture), "GET /public")
			if err != nil {
				t.Fatal(err)
			}
			for _, field := range []string{"auth_header", "auth_env", "test_input_text"} {
				if evidence, ok := inf.ByField(field); ok {
					t.Fatalf("public route inherited protected %s evidence: %+v", field, evidence)
				}
			}
			files := ApplyFiles(inf, true, nil)
			joined := strings.Join(values(files), "\n")
			if strings.Contains(joined, "Authorization") || strings.Contains(joined, "PROTECTED_TOKEN") || strings.Contains(joined, "protected-input") {
				t.Fatalf("public route staged protected evidence:\n%s", joined)
			}
		})
	}
}

func TestRouteEvidenceUsesExactMethodAndPath(t *testing.T) {
	fixtures := []string{
		"java-route-correlation",
		"python-route-correlation",
	}
	for _, fixture := range fixtures {
		t.Run(fixture, func(t *testing.T) {
			for _, selection := range []string{"GET /reports", "GET /public"} {
				inf, err := InferWithRoute(filepath.Join("testdata", fixture), selection)
				if err != nil {
					t.Fatal(err)
				}
				for _, field := range []string{"auth_header", "auth_env", "test_input_text"} {
					if evidence, ok := inf.ByField(field); ok {
						t.Fatalf("%s inherited %s for %s: %+v", selection, field, fixture, evidence)
					}
				}
				joined := strings.Join(values(ApplyFiles(inf, true, nil)), "\n")
				for _, leaked := range []string{"Authorization", "PROTECTED_TOKEN", "protected-input", "protected-result", "admin-result"} {
					if strings.Contains(joined, leaked) {
						t.Fatalf("%s staged %q from another route:\n%s", selection, leaked, joined)
					}
				}
			}
		})
	}
}

func TestSameFileEvidenceUsesMatchedCallRegion(t *testing.T) {
	fixtures := []string{
		"java-route-correlation",
		"python-route-correlation",
	}
	cases := []struct {
		selection string
		expected  string
		protected bool
		forbidden []string
	}{
		{"GET /reports", "public-result", false, []string{"PROTECTED_TOKEN", "protected-input", "protected-result", "admin-result"}},
		{"POST /reports", "protected-result", true, []string{"public-result", "public-page-result", "admin-result"}},
		{"GET /public", "public-page-result", false, []string{"PROTECTED_TOKEN", "protected-input", "protected-result", "admin-result"}},
	}
	for _, fixture := range fixtures {
		t.Run(fixture, func(t *testing.T) {
			for _, tc := range cases {
				inf, err := InferWithRoute(filepath.Join("testdata", fixture), tc.selection)
				if err != nil {
					t.Fatal(err)
				}
				expected, ok := inf.ByField("test_expected")
				if !ok || expected.Value != tc.expected {
					t.Fatalf("%s expected evidence=%+v, want %q evidence=%+v candidates=%+v", tc.selection, expected, tc.expected, inf.Evidence, inf.RouteCandidates)
				}
				_, hasHeader := inf.ByField("auth_header")
				_, hasEnv := inf.ByField("auth_env")
				_, hasInput := inf.ByField("test_input_text")
				if tc.protected != (hasHeader && hasEnv && hasInput) {
					t.Fatalf("%s protected evidence header=%t env=%t input=%t", tc.selection, hasHeader, hasEnv, hasInput)
				}
				joined := strings.Join(values(ApplyFiles(inf, true, nil)), "\n")
				for _, forbidden := range tc.forbidden {
					if strings.Contains(joined, forbidden) {
						t.Fatalf("%s staged %q from another call block:\n%s", tc.selection, forbidden, joined)
					}
				}
			}
		})
	}
}

func TestEvidenceRegionsBindExactCalls(t *testing.T) {
	cases := []struct {
		fixture string
		path    string
		method  string
		want    string
	}{
		{"java-route-correlation/src/test/java/com/example/routes/RouteControllerTest.java", "/reports", "GET", "public-result"},
		{"python-route-correlation/tests/test_routes.py", "/reports", "POST", "protected-result"},
		{"typescript-route-correlation/service/routes.test.ts", "/reports", "GET", "public-result"},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join("testdata", tc.fixture))
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := parserSet{}.parse(tc.fixture, string(body))
			if err != nil {
				t.Fatal(err)
			}
			defer parsed.release()
			var calls []testCallFact
			switch parsed.kind {
			case sourceJava:
				calls = extractJavaTestCalls(parsed)
			case sourcePython:
				calls = extractPythonTestCalls(parsed)
			default:
				calls = extractExpressTestCalls(parsed)
			}
			var matched []testCallFact
			for _, call := range calls {
				if call.route == canonicalRouteIdentity(tc.method, tc.path) {
					matched = append(matched, call)
				}
			}
			if len(matched) != 1 || len(matched[0].assertions) != 1 || matched[0].assertions[0].value != tc.want {
				t.Fatalf("calls=%+v want one %s %s call asserting %q", matched, tc.method, tc.path, tc.want)
			}
		})
	}
}

func TestAdapterDiscoveryUsesSourceFilesAndFiltersOperationalRoutes(t *testing.T) {
	repo := t.TempDir()
	writeFixtureFile(t, repo, "README.md", `app.get("/docs-route", handler)`)
	writeFixtureFile(t, repo, "config/routes.yaml", `path: /yaml-route`)
	writeFixtureFile(t, repo, "node_modules/pkg/index.js", `app.get("/dependency", handler)`)
	writeFixtureFile(t, repo, ".venv/lib/site-packages/pkg/app.py", `@app.get("/venv")`)
	writeFixtureFile(t, repo, "coverage/app.py", `@app.get("/coverage")`)
	writeFixtureFile(t, repo, "services/catalog/app.py", `
from fastapi import FastAPI
app = FastAPI()
@app.get("/health")
def health():
    return {}

@app.get("/reports")
def reports():
    return {}
`)
	inf, err := Infer(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(inf.RouteCandidates) != 1 || inf.RouteCandidates[0].Route() != "GET /reports" {
		t.Fatalf("source adapter candidates=%+v", inf.RouteCandidates)
	}
}

func TestAdapterDiscoveryKeepsDuplicateServiceRoutesDistinct(t *testing.T) {
	repo := t.TempDir()
	writeFixtureFile(t, repo, "services/catalog/app.py", `from fastapi import FastAPI
app = FastAPI()
@app.get("/reports")
def reports(): return {}`)
	writeFixtureFile(t, repo, "services/billing/app.py", `from fastapi import FastAPI
app = FastAPI()
@app.get("/reports")
def reports(): return {}`)
	inf, err := Infer(repo)
	if err != nil {
		t.Fatal(err)
	}
	err = inf.RouteAmbiguity()
	if err == nil || !strings.Contains(err.Error(), "services/catalog") || !strings.Contains(err.Error(), "services/billing") {
		t.Fatalf("duplicate service routes=%+v err=%v", inf.RouteCandidates, err)
	}
}

func TestAdapterCandidateIdentityIncludesServiceAndSource(t *testing.T) {
	repo := t.TempDir()
	for _, service := range []string{"catalog", "billing"} {
		writeFixtureFile(t, repo, "services/"+service+"/app.py", `
from fastapi import FastAPI
app = FastAPI()
@app.get("/reports")
def reports():
    return {}
`)
	}

	inf, err := Infer(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(inf.RouteCandidates) != 2 {
		t.Fatalf("candidates=%+v", inf.RouteCandidates)
	}
	for _, candidate := range inf.RouteCandidates {
		if !strings.Contains(candidate.Identity(), candidate.ServiceRoot) || !strings.Contains(candidate.Identity(), candidate.Source) {
			t.Fatalf("identity %q does not qualify service/source for %+v", candidate.Identity(), candidate)
		}
	}
}

func TestAdapterPreservesDuplicateRoutesFromSameService(t *testing.T) {
	repo := t.TempDir()
	writeFixtureFile(t, repo, "service/server.ts", `
import express from "express"
const app = express()
app.post("/reports", first)
app.post("/reports", second)
`)

	inf, err := Infer(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(inf.RouteCandidates) != 2 || inf.RouteAmbiguity() == nil {
		t.Fatalf("duplicate routes were collapsed: %+v", inf.RouteCandidates)
	}
	if inf.RouteCandidates[0].Source == inf.RouteCandidates[1].Source {
		t.Fatalf("duplicate candidates lost source context: %+v", inf.RouteCandidates)
	}
}

func TestEvidenceBindsIndividualRequestExpressions(t *testing.T) {
	repo := t.TempDir()
	writeFixtureFile(t, repo, "service/server.ts", `
import express from "express"
const app = express()
app.post("/public", handler)
app.post("/private", handler)
export default app
`)
	writeFixtureFile(t, repo, "service/routes.test.ts", `
import request from "supertest"
import app from "./server"
test("both", () => {
  request(app).post("/public").send({ text: "public-input" }).expect({ text: "public-output" })
  request(app).post("/private").set("Authorization", process.env.PRIVATE_TOKEN).send({ text: "private-input" }).expect({ text: "private-output" })
})
`)

	inf, err := InferWithRoute(repo, "POST /public")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"Authorization", "PRIVATE_TOKEN", "private-input", "private-output"} {
		if strings.Contains(fmt.Sprintf("%+v", inf.Evidence), forbidden) {
			t.Fatalf("public route inherited private evidence %q: %+v", forbidden, inf.Evidence)
		}
	}
	requireHigh(t, inf, "test_input_text", "public-input")
	requireHigh(t, inf, "test_expected", "public-output")
}

func TestRootTestEvidenceDoesNotBindChildService(t *testing.T) {
	repo := t.TempDir()
	writeFixtureFile(t, repo, "services/catalog/server.ts", `
import express from "express"
const app = express()
app.post("/reports", handler)
`)
	writeFixtureFile(t, repo, "tests/catalog.test.ts", `
test("catalog", () => request(app).post("/reports").set("Authorization", process.env.ROOT_TOKEN).send({ text: "root-input" }))
`)

	inf, err := Infer(repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"auth_header", "auth_env", "test_input_text"} {
		if evidence, ok := inf.ByField(forbidden); ok {
			t.Fatalf("root test evidence leaked into child service: %+v", evidence)
		}
	}
}

func TestJavaServicesUseServiceScopedIdentityOverPackageSuffix(t *testing.T) {
	repo := t.TempDir()
	for _, service := range []string{"catalog", "billing"} {
		writeFixtureFile(t, repo, "services/"+service+"/src/main/java/com/example/api/ReportsController.java", `
package com.example.api;
import org.springframework.web.bind.annotation.*;
@RequestMapping("/reports")
class ReportsController {
  @PostMapping("/analyze")
  Reply analyze(Request request) { return null; }
}
`)
	}

	inf, err := Infer(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(inf.RouteCandidates) != 2 || inf.RouteAmbiguity() == nil {
		t.Fatalf("same-package service routes were not ambiguous: %+v", inf.RouteCandidates)
	}
	if inf.RouteCandidates[0].Agent == inf.RouteCandidates[1].Agent {
		t.Fatalf("Java candidates retained package-derived agent identity: %+v", inf.RouteCandidates)
	}
}

func TestJavaAdaptersIgnoreCommentsAndSupportMappingForms(t *testing.T) {
	repo := t.TempDir()
	writeFixtureFile(t, repo, "src/main/java/example/CatalogController.java", `
package example;
import org.springframework.web.bind.annotation.*;
// @GetMapping("/comment-only")
@RequestMapping(value = "/api")
class CatalogController {
  @GetMapping(path = "/items")
  Response items() { return null; }
}`)
	inf, err := InferWithRoute(repo, "get /api/items/")
	if err != nil {
		t.Fatal(err)
	}
	requireHigh(t, inf, "endpoint_path", "/api/items")
	if len(inf.RouteCandidates) != 1 || strings.Contains(inf.RouteCandidates[0].Path, "comment") {
		t.Fatalf("mapping candidates=%+v", inf.RouteCandidates)
	}
}

func TestJavaFrameworkProofRejectsCommentsAndStrings(t *testing.T) {
	repo := t.TempDir()
	writeFixtureFile(t, repo, "src/main/java/example/Lookalike.java", `
package example;
// import org.springframework.web.bind.annotation.GetMapping;
/* import org.springframework.web.bind.annotation.RequestMapping; */
class Lookalike {
  String framework = "org.springframework.web.bind.annotation.RequestMapping";
  @RequestMapping("/api")
  @PostMapping("/unsafe")
  Reply unsafe(Request request) { return null; }
}
`)

	inf, err := Infer(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(inf.RouteCandidates) != 0 || inf.HasSignal || ApplyFiles(inf, true, nil) != nil {
		t.Fatalf("comment or string Java framework proof became stageable: %+v", inf)
	}
}

func TestExpressAdapterRejectsCommentOnlyStatements(t *testing.T) {
	repo := t.TempDir()
	writeFixtureFile(t, repo, "src/server.ts", `
// import express from "express";
// const app = express();
// app.post("/line-comment", handler);
/*
const router = express.Router();
router.post("/block-comment", handler);
*/
`)

	inf, err := Infer(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(inf.RouteCandidates) != 0 || inf.HasSignal || ApplyFiles(inf, true, nil) != nil {
		t.Fatalf("comment-only Express statements became stageable: %+v", inf)
	}
}

func TestAdaptersMaskEmbeddedRouteLookingStrings(t *testing.T) {
	cases := []struct {
		name string
		path string
		body string
		want string
	}{
		{
			name: "java text block",
			path: "src/main/java/example/Controller.java",
			body: `
package example;
import org.springframework.web.bind.annotation.*;
class Controller {
  String fake = """
import org.springframework.web.bind.annotation.*;
@PostMapping("/embedded")
Reply embedded(Request request) { return null; }
""";
  @PostMapping("/real")
  Reply real(Request request) { return null; }
}
`,
			want: "POST /real",
		},
		{
			name: "typescript template literal",
			path: "src/server.ts",
			body: "\nconst fake = `\n" +
				"import express from \"express\"\n" +
				"const embedded = express()\n" +
				"embedded.post(\"/embedded\", handler)\n" +
				"`\n" +
				"const continuedFake = 'ignore\\\n" +
				"import express from \"express\"\n" +
				"const continued = express()\n" +
				"continued.post(\"/continued-embedded\", handler)\n" +
				"'\n" +
				"import express from \"express\"\n" +
				"const app = express()\n" +
				"app.post(\"/real\", handler)\n",
			want: "POST /real",
		},
		{
			name: "python triple quoted string",
			path: "src/main.py",
			body: `
fake = """
from fastapi import FastAPI
embedded = FastAPI()
@embedded.post("/embedded")
def embedded_route(): return {}
"""
continued_fake = "ignore\
from fastapi import FastAPI\
continued = FastAPI()\
@continued.post('/continued-embedded')\
def continued_embedded_route(): return {}\
"
from fastapi import FastAPI
app = FastAPI()
@app.post("/real")
def real_route(): return {}
`,
			want: "POST /real",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			writeFixtureFile(t, repo, tc.path, tc.body)

			inf, err := Infer(repo)
			if err != nil {
				t.Fatal(err)
			}
			if len(inf.RouteCandidates) != 1 || inf.RouteCandidates[0].Route() != tc.want {
				t.Fatalf("embedded source generated candidates=%+v, want only %q", inf.RouteCandidates, tc.want)
			}
		})
	}
}

func TestMaskedAdapterEvidenceIgnoresEmbeddedSelectedRouteCalls(t *testing.T) {
	cases := []struct {
		name       string
		sourcePath string
		sourceBody string
		testPath   string
		testBody   string
	}{
		{
			name:       "java",
			sourcePath: "src/main/java/example/Controller.java",
			sourceBody: `
package example;
import org.springframework.web.bind.annotation.*;
class Controller {
  @PostMapping("/reports")
  Reply reports(@RequestHeader("Authorization") String authorization) { return null; }
}
`,
			testPath: "src/test/java/example/ControllerTest.java",
			testBody: `
import org.springframework.test.web.servlet.MockMvc;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;
class ControllerTest {
  MockMvc mockMvc;
  void reports() {
    String fake = """
mockMvc.perform(post("/reports").header("Authorization", System.getenv("FAKE_TOKEN")).content("{\"text\":\"fake-input\"}")).andExpect(jsonPath("$.text").value("fake-output"));
""";
    // mockMvc.perform(post("/reports").header("Authorization", System.getenv("COMMENT_TOKEN")).content("{\"text\":\"comment-input\"}")).andExpect(jsonPath("$.text").value("comment-output"));
    mockMvc.perform(post("/reports").header("Authorization", System.getenv("REAL_TOKEN")).content("{\"text\":\"real-input\"}")).andExpect(jsonPath("$.text").value("real-output"));
  }
}
`,
		},
		{
			name:       "typescript",
			sourcePath: "service/server.ts",
			sourceBody: `
import express from "express"
const app = express()
app.post("/reports", handler)
export default app
`,
			testPath: "service/routes.test.ts",
			testBody: "\nimport request from \"supertest\"\nimport app from \"./server\"\nconst fake = `\n" +
				"request(app).post(\"/reports\").set(\"Authorization\", process.env.FAKE_TOKEN).send({ text: \"fake-input\" }).expect({ text: \"fake-output\" })\n" +
				"`\n" +
				"// request(app).post(\"/reports\").set(\"Authorization\", process.env.COMMENT_TOKEN).send({ text: \"comment-input\" }).expect({ text: \"comment-output\" })\n" +
				"request(app).post(\"/reports\").set(\"Authorization\", process.env.REAL_TOKEN).send({ text: \"real-input\" }).expect({ text: \"real-output\" })\n",
		},
		{
			name:       "python",
			sourcePath: "service/main.py",
			sourceBody: `
from fastapi import FastAPI
app = FastAPI()
@app.post("/reports")
def reports(): return {}
`,
			testPath: "service/tests/test_routes.py",
			testBody: `
from fastapi.testclient import TestClient
from service.main import app
client = TestClient(app)
fake = """
client.post("/reports", headers={"Authorization": os.environ["FAKE_TOKEN"]}, json={"text": "fake-input"})
assert fake.json()["text"] == "fake-output"
"""
# client.post("/reports", headers={"Authorization": os.environ["COMMENT_TOKEN"]}, json={"text": "comment-input"})
response = client.post("/reports", headers={"Authorization": os.environ["REAL_TOKEN"]}, json={"text": "real-input"})
assert response.json()["text"] == "real-output"
`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			writeFixtureFile(t, repo, tc.sourcePath, tc.sourceBody)
			writeFixtureFile(t, repo, tc.testPath, tc.testBody)

			inf, err := Infer(repo)
			if err != nil {
				t.Fatal(err)
			}
			for _, expected := range [][2]string{
				{"auth_env", "REAL_TOKEN"},
				{"test_input_text", "real-input"},
				{"test_expected", "real-output"},
			} {
				requireHigh(t, inf, expected[0], expected[1])
			}
			for _, forbidden := range []string{"FAKE_TOKEN", "COMMENT_TOKEN", "fake-input", "comment-input", "fake-output", "comment-output"} {
				if strings.Contains(fmt.Sprintf("%+v", inf.Evidence), forbidden) {
					t.Fatalf("embedded evidence leaked %q: %+v", forbidden, inf.Evidence)
				}
			}
		})
	}
}

func TestMaskedPortInferenceIgnoresEmbeddedCode(t *testing.T) {
	cases := []struct {
		name string
		path string
		body string
		want string
	}{
		{
			name: "java comments and text blocks",
			path: "src/main/java/example/Controller.java",
			body: `
package example;
import org.springframework.web.bind.annotation.*;
/*
server.port=7777
*/
class Controller {
  String fake = """
server.port=9999
""";
  @PostMapping("/reports")
  Reply reports(Request request) { return null; }
}
`,
			want: "8080",
		},
		{
			name: "typescript comments and strings",
			path: "service/server.ts",
			body: `
import express from "express"
// app.listen(7777)
const fake = "app.listen(9999)"
const app = express()
app.post("/reports", handler)
app.listen(3010)
`,
			want: "3010",
		},
		{
			name: "python comments and strings",
			path: "service/main.py",
			body: `
import uvicorn
from fastapi import FastAPI
# uvicorn.run(app, port=7777)
fake = """
uvicorn.run(app, port=9999)
"""
app = FastAPI()
@app.post("/reports")
def reports(): return {}
uvicorn.run(app, port=8010)
`,
			want: "8010",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			writeFixtureFile(t, repo, tc.path, tc.body)
			if tc.name == "java comments and text blocks" {
				writeFixtureFile(t, repo, "src/main/resources/application.properties", "server.port=8088\n")
			}

			inf, err := Infer(repo)
			if err != nil {
				t.Fatal(err)
			}
			requireHigh(t, inf, "port", tc.want)
		})
	}
}

func TestJavaAuthEnvBindsIndividualRequestExpression(t *testing.T) {
	repo := t.TempDir()
	writeFixtureFile(t, repo, "src/main/java/example/RouteController.java", `
package example;
import org.springframework.web.bind.annotation.*;
class RouteController {
  @GetMapping("/public")
  Reply publicRoute(@RequestHeader(value = "Authorization", required = false) String authorization) { return null; }

  @PostMapping("/private")
  Reply privateRoute(@RequestHeader("Authorization") String authorization) { return null; }
}
`)
	writeFixtureFile(t, repo, "src/test/java/example/RouteControllerTest.java", `
package example;
import org.springframework.test.web.servlet.MockMvc;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;
class RouteControllerTest {
    MockMvc mockMvc;
    void routes() {
        mockMvc.perform(get("/public")).andExpect(jsonPath("$.text").value("public-result"));
        mockMvc.perform(post("/private").header("Authorization", System.getenv("PRIVATE_TOKEN"))).andExpect(jsonPath("$.text").value("private-result"));
    }
}
`)

	inf, err := InferWithRoute(repo, "GET /public")
	if err != nil {
		t.Fatal(err)
	}
	requireHigh(t, inf, "auth_header", "Authorization")
	if evidence, ok := inf.ByField("auth_env"); ok {
		t.Fatalf("public request inherited protected auth env: %+v", evidence)
	}
}

func TestJavaAdapterSupportsPackagePrivateJaxRSMethods(t *testing.T) {
	repo := t.TempDir()
	writeFixtureFile(t, repo, "src/main/java/example/Resource.java", `
package example;
import javax.ws.rs.*;
@Path("/api")
class Resource {
  @GET
  @Path("/reports")
  Report reports() { return null; }
}`)
	inf, err := Infer(repo)
	if err != nil {
		t.Fatal(err)
	}
	requireHigh(t, inf, "endpoint_path", "/api/reports")
	requireHigh(t, inf, "method", "GET")
}

func TestJavaAdapterRejectsUnprovenFrameworkAnnotations(t *testing.T) {
	repo := t.TempDir()
	writeFixtureFile(t, repo, "src/main/java/example/Lookalike.java", `
package example;
@RequestMapping("/api")
class Lookalike {
  @PostMapping("/unsafe")
  Reply unsafe(Request request) { return null; }
}
`)

	inf, err := Infer(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(inf.RouteCandidates) != 0 || inf.HasSignal || ApplyFiles(inf, true, nil) != nil {
		t.Fatalf("unproven Java annotations became stageable: %+v", inf)
	}
}

func TestJavaAdapterRequiresFrameworkProofInAnnotatedFile(t *testing.T) {
	repo := t.TempDir()
	writeFixtureFile(t, repo, "src/main/java/example/Application.java", `
package example;
import org.springframework.boot.autoconfigure.SpringBootApplication;
@SpringBootApplication
class Application {}
`)
	writeFixtureFile(t, repo, "src/main/java/example/Lookalike.java", `
package example;
@RequestMapping("/api")
class Lookalike {
  @PostMapping("/unsafe")
  Reply unsafe(Request request) { return null; }
}
`)

	inf, err := Infer(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(inf.RouteCandidates) != 0 {
		t.Fatalf("service-wide bootstrap accepted lookalike annotations: %+v", inf.RouteCandidates)
	}
}

func TestPythonEvidenceBindsIndividualRequestCalls(t *testing.T) {
	repo := t.TempDir()
	writeFixtureFile(t, repo, "service/main.py", `
from fastapi import FastAPI
app = FastAPI()
@app.get("/public")
def public(): return {}
@app.post("/private")
def private(): return {}
`)
	writeFixtureFile(t, repo, "service/tests/test_routes.py", `
from fastapi.testclient import TestClient
from service.main import app
client = TestClient(app)
def test_both():
    public = client.get("/public")
    assert public.json()["text"] == "public-output"
    private = client.post("/private", headers={"Authorization": "secret"}, json={"text": "private-input"})
    assert private.json()["text"] == "private-output"
`)

	inf, err := InferWithRoute(repo, "GET /public")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"Authorization", "private-input", "private-output"} {
		if strings.Contains(fmt.Sprintf("%+v", inf.Evidence), forbidden) {
			t.Fatalf("public call inherited private Python evidence %q: %+v", forbidden, inf.Evidence)
		}
	}
	requireHigh(t, inf, "test_expected", "public-output")
}

func TestPortEvidenceExcludesTestsAndDocumentation(t *testing.T) {
	repo := t.TempDir()
	writeFixtureFile(t, repo, "service/server.ts", `
import express from "express"
const app = express()
app.post("/reports", handler)
`)
	writeFixtureFile(t, repo, "service/tests/routes.test.ts", `app.listen(9911)`)
	writeFixtureFile(t, repo, "docs/example.ts", `app.listen(9922)`)

	inf, err := Infer(repo)
	if err != nil {
		t.Fatal(err)
	}
	requireHigh(t, inf, "port", "8080")
}

func TestUnsupportedRouteSyntaxNeverCreatesOrStagesCandidates(t *testing.T) {
	repo := t.TempDir()
	writeFixtureFile(t, repo, "src/unknown.js", `
const app = createFramework();
app.post("/arbitrary", handler);
const router = makeUnknownRouter();
router.post("/router-arbitrary", handler);
`)
	writeFixtureFile(t, repo, "src/unknown.py", `
class App:
    def post(self, path):
        return lambda handler: handler
app = App()
@app.post("/arbitrary")
def arbitrary():
    return {}
`)

	inf, err := Infer(repo)
	if err != nil {
		t.Fatal(err)
	}
	if inf.HasSignal || len(inf.RouteCandidates) != 0 || ApplyFiles(inf, true, nil) != nil {
		t.Fatalf("unsupported syntax became stageable: %+v", inf)
	}

	code, err := Setup(SetupOptions{Repo: repo, Stdout: io.Discard, Stderr: io.Discard})
	if code != 0 || err != nil {
		t.Fatalf("code=%d err=%v", code, err)
	}
	assertNoEvalFiles(t, repo)
	report, code, err := Scan(repo, "")
	if err != nil || code != 0 || report.HasSignal || len(report.Candidates) != 0 {
		t.Fatalf("code=%d err=%v report=%+v", code, err, report)
	}
}

func TestSupportedAdaptersRequireFrameworkMarkers(t *testing.T) {
	repo := t.TempDir()
	writeFixtureFile(t, repo, "unsupported/route.ts", `
const app = makeUnknown();
app.post("/unknown", handler);
`)
	writeFixtureFile(t, repo, "fastapi/main.py", `
from fastapi import FastAPI
app = FastAPI()
@app.post("/fast")
def fast():
    return {}
`)
	writeFixtureFile(t, repo, "express/server.ts", `
import express from "express"
const app = express()
app.post("/express", handler)
`)
	inf, err := Infer(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(inf.RouteCandidates) != 2 {
		t.Fatalf("recognized candidates=%+v", inf.RouteCandidates)
	}
	for _, candidate := range inf.RouteCandidates {
		if candidate.Path == "/unknown" {
			t.Fatalf("generic app method was treated as Express: %+v", candidate)
		}
	}
}

func TestJavaRequestMappingVariantsAndModifiers(t *testing.T) {
	repo := t.TempDir()
	writeFixtureFile(t, repo, "src/main/java/example/MappingController.java", `
package example;
import org.springframework.web.bind.annotation.*;
@RequestMapping(path = "/api")
class MappingController {
  @RequestMapping(value = "/final", method = RequestMethod.POST)
  public final Reply finalReply(Request request) { return null; }

  @RequestMapping(path = "/package", method = RequestMethod.PUT)
  Reply packageReply(Request request) { return null; }
}
`)
	for _, want := range []string{"POST /api/final", "PUT /api/package"} {
		inf, err := InferWithRoute(repo, want)
		if err != nil {
			t.Fatal(err)
		}
		method, path := strings.SplitN(want, " ", 2)[0], strings.SplitN(want, " ", 2)[1]
		requireHigh(t, inf, "method", method)
		requireHigh(t, inf, "endpoint_path", path)
	}
}

func TestJavaRequestMappingEnumeratesEveryExplicitMethod(t *testing.T) {
	repo := t.TempDir()
	writeFixtureFile(t, repo, "src/main/java/example/MappingController.java", `
package example;
import org.springframework.web.bind.annotation.*;
@RequestMapping("/api")
class MappingController {
  @RequestMapping(path = "/items", method = {RequestMethod.POST, RequestMethod.PUT})
  public final Reply update(Request request) { return null; }
}
`)

	inf, err := Infer(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(inf.RouteCandidates) != 2 {
		t.Fatalf("candidates=%+v", inf.RouteCandidates)
	}
	for _, route := range []string{"POST /api/items", "PUT /api/items"} {
		found := false
		for _, candidate := range inf.RouteCandidates {
			found = found || candidate.Route() == route
		}
		if !found {
			t.Fatalf("missing %q from %+v", route, inf.RouteCandidates)
		}
	}
}

func TestServiceScopedDiscoveryKeepsStacksAndDTOsIndependent(t *testing.T) {
	repo := t.TempDir()
	writeFixtureFile(t, repo, "services/catalog/src/main/java/catalog/CatalogController.java", `
package catalog;
import org.springframework.web.bind.annotation.*;
@RequestMapping("/catalog")
class CatalogController {
  @PostMapping("/analyze")
  public CatalogResponse analyze(CatalogRequest request) { return null; }
}
class CatalogRequest { private String catalogText; }
class CatalogResponse { private String catalogResult; }
`)
	writeFixtureFile(t, repo, "services/billing/src/main/java/billing/BillingController.java", `
package billing;
import org.springframework.web.bind.annotation.*;
@RequestMapping("/billing")
class BillingController {
  @PostMapping("/analyze")
  BillingResponse analyze(BillingRequest request) { return null; }
}
class BillingRequest { private String billingText; }
class BillingResponse { private String billingResult; }
`)
	writeFixtureFile(t, repo, "services/web/server.ts", `
import express from "express"
const app = express()
app.post("/analyze", handler)
`)

	all, err := Infer(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(all.RouteCandidates) != 3 {
		t.Fatalf("mixed-service candidates=%+v", all.RouteCandidates)
	}
	if all.RouteAmbiguity() == nil {
		t.Fatal("same method/path in different service roots must remain ambiguous")
	}

	catalog, err := InferWithRoute(repo, "POST /catalog/analyze")
	if err != nil {
		t.Fatal(err)
	}
	requireHigh(t, catalog, "request_field", "catalogText")
	requireHigh(t, catalog, "response_path", "catalogResult")
	billing, err := InferWithRoute(repo, "POST /billing/analyze")
	if err != nil {
		t.Fatal(err)
	}
	requireHigh(t, billing, "request_field", "billingText")
	requireHigh(t, billing, "response_path", "billingResult")
}

func TestAdapterDiscoverySkipsGeneratedDirectoriesAndPrefixedOperationalEndpoints(t *testing.T) {
	repo := t.TempDir()
	for _, dir := range []string{
		"generated", "build", "dist", "target", "vendor", "node_modules", ".venv",
		"venv", "site-packages", "__pycache__", ".next", "coverage", ".gradle",
	} {
		writeFixtureFile(t, repo, filepath.Join(dir, "server.ts"), `
import express from "express"
const app = express()
app.post("/generated", handler)
`)
	}
	writeFixtureFile(t, repo, "docs/example.ts", `
import express from "express"
const app = express()
app.post("/docs-only", handler)
`)
	writeFixtureFile(t, repo, "tests/example.ts", `
import express from "express"
const app = express()
app.post("/test-only", handler)
`)
	writeFixtureFile(t, repo, "src/server.ts", `
import express from "express"
const app = express()
app.get("/api/work/health", handler)
app.get("/api/work/ready/", handler)
app.get("/api/work/metrics", handler)
app.post("/api/work/analyze", handler)
`)

	inf, err := Infer(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(inf.RouteCandidates) != 1 || inf.RouteCandidates[0].Route() != "POST /api/work/analyze" {
		t.Fatalf("source candidates=%+v", inf.RouteCandidates)
	}
}

func TestEvidenceIndexScansEachTestFileOnce(t *testing.T) {
	repo := t.TempDir()
	var routes strings.Builder
	for i := 0; i < 12; i++ {
		fmt.Fprintf(&routes, "app.post(\"/route-%d\", handler)\n", i)
	}
	writeFixtureFile(t, repo, "src/server.ts", "import express from \"express\"\nconst app = express()\n"+routes.String())
	for i := 0; i < 5; i++ {
		writeFixtureFile(t, repo, fmt.Sprintf("tests/route-%d.test.ts", i), fmt.Sprintf(`
test("route", () => request(app).post("/route-%d").send({ text: "value" }))
`, i))
	}
	inf, err := Infer(repo)
	if err != nil {
		t.Fatal(err)
	}
	field := reflect.ValueOf(inf).FieldByName("EvidenceFilesScanned")
	if !field.IsValid() || field.Kind() != reflect.Int || field.Int() != 5 {
		t.Fatalf("evidence scans=%v, want exactly one scan per test file", field)
	}
}

func TestDeprecatedSkipValidatorReturnsError(t *testing.T) {
	code, err := Setup(SetupOptions{Repo: t.TempDir(), SkipValidator: true, Stdout: io.Discard, Stderr: io.Discard})
	if code != 1 || err == nil || !strings.Contains(err.Error(), "deprecated") {
		t.Fatalf("code=%d err=%v", code, err)
	}
}

func TestExplicitRouteMustMatchSingleCandidate(t *testing.T) {
	repo := copyFixture(t, "java-support")
	_, code, err := Scan(repo, "POST /wrong")
	if code != 2 || err == nil || !strings.Contains(err.Error(), "not a supported AST-detected route") {
		t.Fatalf("code=%d err=%v", code, err)
	}
	assertNoEvalFiles(t, repo)
}

func TestUnsupportedRouteSelectionRequiresGuidedManualConfiguration(t *testing.T) {
	repo := t.TempDir()
	writeFixtureFile(t, repo, "server.ts", `const app = { post() {} }
app.post("/unsupported", handler)
`)
	_, code, err := Scan(repo, "POST /unsupported")
	if code != 2 || err == nil || !strings.Contains(err.Error(), "is not a supported AST-detected route") ||
		!strings.Contains(err.Error(), "guided/manual configuration") {
		t.Fatalf("code=%d err=%v", code, err)
	}
	assertNoEvalFiles(t, repo)
}

func TestSpringRouteInfersQueryAndPathParameters(t *testing.T) {
	inf := inferFixture(t, "java-spring-params")
	requireHigh(t, inf, "endpoint_path", "/api/catalog/items/{itemId}/analyze")
	requireHigh(t, inf, "required_path_params", "itemId")
	requireHigh(t, inf, "path_param_mappings", "itemId:item")
	requireHigh(t, inf, "required_query_params", "includeDetails")
	requireHigh(t, inf, "query_param_mappings", "includeDetails:details")
	requireHigh(t, inf, "request_fields", "query")
}

func TestMicronautRouteInfersQueryAndPathParameters(t *testing.T) {
	inf := inferFixture(t, "java-micronaut-params")
	requireHigh(t, inf, "endpoint_path", "/api/inventory/items/{itemId}")
	requireHigh(t, inf, "required_path_params", "itemId")
	requireHigh(t, inf, "path_param_mappings", "itemId:itemId")
	requireHigh(t, inf, "required_query_params", "view")
	requireHigh(t, inf, "query_param_mappings", "view:responseView")
	requireHigh(t, inf, "request_fields", "query")
}

func TestJaxRSRouteInfersQueryAndPathParameters(t *testing.T) {
	inf := inferFixture(t, "java-jaxrs-params")
	requireHigh(t, inf, "endpoint_path", "/api/reports/{reportId}")
	requireHigh(t, inf, "required_path_params", "reportId")
	requireHigh(t, inf, "path_param_mappings", "reportId:id")
	requireHigh(t, inf, "required_query_params", "include")
	requireHigh(t, inf, "query_param_mappings", "include:expanded")
	requireHigh(t, inf, "request_fields", "query")
}

func TestAmbiguousJavaRoutesRequireExplicitSelectionBeforeStaging(t *testing.T) {
	repo := copyFixture(t, "java-multi-route")
	report, code, err := Scan(repo, "")
	if code != 0 || err != nil {
		t.Fatalf("code=%d err=%v", code, err)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	if !strings.Contains(text, "/api/v1/incidents/analyze") || !strings.Contains(text, "/api/v1/summaries/summarize") {
		t.Fatalf("scan JSON = %s", text)
	}
	assertNoEvalFiles(t, repo)
}

func TestDeprecatedSkipValidatorCannotBypassValidation(t *testing.T) {
	repo := copyFixture(t, "java-support")
	code, err := Setup(SetupOptions{
		Repo:          repo,
		SkipValidator: true,
		Stdout:        io.Discard,
		Stderr:        io.Discard,
	})
	if code != 1 || err == nil || !strings.Contains(err.Error(), "deprecated") {
		t.Fatalf("code=%d err=%v", code, err)
	}
	assertNoEvalFiles(t, repo)
}

func TestStageCollisionIdempotenceAndValidatorArgs(t *testing.T) {
	repo := copyFixture(t, "java-support")
	inf := inferFixture(t, "java-support")
	var calls [][]string
	run := func(name string, args []string, env []string, dir string) (CommandResult, error) {
		calls = append(calls, append([]string{name}, args...))
		return CommandResult{}, nil
	}
	first, err := Stage(repo, inf.Files, "python-test", run)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Wrote) != 3 || len(calls) != 1 {
		t.Fatalf("wrote=%v calls=%v", first.Wrote, calls)
	}
	got := calls[0]
	if len(got) != 5 || got[0] != "python-test" || got[1] != "-I" || got[2] != "-c" || got[3] != ValidatorSnippet || !strings.HasSuffix(got[4], filepath.Join("repo", ".harness", "evals")) {
		t.Fatalf("validator args = %#v", got)
	}
	agent := filepath.Join(repo, ".harness", "evals", "agents", "support", "agent.yaml")
	info, err := os.Stat(agent)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Stage(repo, inf.Files, "python-test", run)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Wrote) != 0 || len(second.Identical) != 3 {
		t.Fatalf("second stage = %+v", second)
	}
	again, err := os.Stat(agent)
	if err != nil {
		t.Fatal(err)
	}
	if !again.ModTime().Equal(info.ModTime()) {
		t.Fatal("identical file was rewritten")
	}

	colliding := copyFixture(t, "java-support")
	dest := filepath.Join(colliding, ".harness", "evals", "agents", "support", "agent.yaml")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	const original = "identifier: keep-me\n"
	if err := os.WriteFile(dest, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = Stage(colliding, inf.Files, "python-test", run)
	if err == nil || !strings.Contains(err.Error(), "agents/support/agent.yaml") {
		t.Fatalf("collision err = %v", err)
	}
	kept, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(kept) != original {
		t.Fatalf("collision overwrote destination: %s", kept)
	}
	if _, err := os.Stat(filepath.Join(colliding, ".harness", "evals", "datasets", "support.jsonl")); err != nil {
		t.Fatal("missing non-colliding file was not written")
	}
}

func TestStageUsesPinnedSchemaBeforeWriting(t *testing.T) {
	layout := RunnerLayout{Python: "validator"}
	inf := inferFixture(t, "java-support")
	files := make(map[string]string, len(inf.Files))
	for path, body := range inf.Files {
		files[path] = body
	}
	agentPath := "agents/support/agent.yaml"
	files[agentPath] = strings.Replace(files[agentPath], "      ref: support-token\n", "", 1)
	repo := t.TempDir()
	validate := func(string, []string, []string, string) (CommandResult, error) {
		return CommandResult{Code: 1, Stderr: "harness_secret source requires ref"}, nil
	}
	if _, err := Stage(repo, files, layout.Python, validate); err == nil || !strings.Contains(err.Error(), "harness_secret source requires ref") {
		t.Fatalf("pinned schema validation error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".harness", "evals", agentPath)); !os.IsNotExist(err) {
		t.Fatalf("invalid tree was written before validation, stat err=%v", err)
	}
}

func TestSetupDoesNotWriteEvalFiles(t *testing.T) {
	repo := copyFixture(t, "java-support")
	code, err := Setup(SetupOptions{Repo: repo, Stdout: io.Discard, Stderr: io.Discard})
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	assertNoEvalFiles(t, repo)
}

func TestWriteCandidateValidatesAndWrites(t *testing.T) {
	layout := RunnerLayout{Python: "validator"}
	repo := copyFixture(t, "java-support")
	report, code, err := Scan(repo, "")
	if err != nil || code != 0 || len(report.Candidates) != 1 {
		t.Fatalf("code=%d err=%v candidates=%d", code, err, len(report.Candidates))
	}
	assertNoEvalFiles(t, repo)
	candidate := report.Candidates[0].AsCandidate()
	candidate.Cases = validSampleCases()
	code, err = WriteCandidate(WriteOptions{
		Repo:      repo,
		Candidate: candidate,
		Confirm:   true,
		Python:    layout.Python,
		Stdout:    io.Discard,
		Stderr:    io.Discard,
	})
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".harness", "evals", "agents", "support", "agent.yaml")); err != nil {
		t.Fatal(err)
	}
}

func TestExplicitJavaRouteSelectionStagesPinnedSchemaTree(t *testing.T) {
	layout := RunnerLayout{Python: "validator"}
	repo := copyFixture(t, "java-multi-route")
	report, code, err := Scan(repo, "POST /api/v1/incidents/analyze")
	if err != nil || code != 0 || len(report.Candidates) != 1 {
		t.Fatalf("code=%d err=%v candidates=%d", code, err, len(report.Candidates))
	}
	assertNoEvalFiles(t, repo)
	candidate := report.Candidates[0].AsCandidate()
	candidate.Cases = validSampleCases()
	code, err = WriteCandidate(WriteOptions{
		Repo:      repo,
		Candidate: candidate,
		Confirm:   true,
		Python:    layout.Python,
		Stdout:    io.Discard,
		Stderr:    io.Discard,
	})
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	agent := filepath.Join(repo, ".harness", "evals", "agents", "incidents", "agent.yaml")
	if _, err := os.Stat(agent); err != nil {
		t.Fatal(err)
	}
}

func TestMethodQualifiedRouteSelectionIsRequired(t *testing.T) {
	repo := copyFixture(t, "java-multi-route")
	_, code, err := Scan(repo, "/api/v1/incidents/analyze")
	if code != 2 || err == nil || !strings.Contains(err.Error(), "POST /api/v1/incidents/analyze") {
		t.Fatalf("code=%d err=%v", code, err)
	}
	assertNoEvalFiles(t, repo)
}

func TestSpringAndMicronautFixturesStageWithPinnedRunner(t *testing.T) {
	layout := RunnerLayout{Python: "validator"}
	for _, fixture := range []string{"java-spring-params", "java-micronaut-params"} {
		t.Run(fixture, func(t *testing.T) {
			repo := copyFixture(t, fixture)
			report, code, err := Scan(repo, "")
			if err != nil || code != 0 || len(report.Candidates) != 1 {
				t.Fatalf("code=%d err=%v candidates=%d", code, err, len(report.Candidates))
			}
			candidate := report.Candidates[0].AsCandidate()
			candidate.Cases = validSampleCases()
			code, err = WriteCandidate(WriteOptions{
				Repo:      repo,
				Candidate: candidate,
				Confirm:   true,
				Python:    layout.Python,
				Stdout:    io.Discard,
				Stderr:    io.Discard,
			})
			if code != 0 || err != nil {
				t.Fatalf("code=%d err=%v", code, err)
			}
		})
	}
}

func TestWriteCandidateKeepsDatasetJSONValid(t *testing.T) {
	layout := RunnerLayout{Python: "validator"}
	repo := t.TempDir()
	code, err := WriteCandidate(WriteOptions{
		Repo: repo,
		Candidate: Candidate{
			AgentID:      "guide",
			EndpointURL:  "http://127.0.0.1:8080/analyze",
			Method:       "POST",
			RequestField: "query",
			ResponsePath: "reasoning",
			Cases: []SampleCase{{
				ID:       "case-1",
				Input:    json.RawMessage(`{"query":"hello"}`),
				Expected: "ok",
			}},
		},
		Confirm: true,
		Python:  layout.Python,
		Stdout:  io.Discard,
		Stderr:  io.Discard,
	})
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".harness", "evals", ".onboard-guide-draft")); !os.IsNotExist(err) {
		t.Fatalf("draft marker stat err=%v", err)
	}
	dataset, err := os.ReadFile(filepath.Join(repo, ".harness", "evals", "datasets", "guide.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(dataset)), "\n") {
		if !json.Valid([]byte(line)) {
			t.Fatalf("dataset line is not valid JSON: %q", line)
		}
	}
	if !strings.Contains(string(dataset), `"query":"hello"`) {
		t.Fatalf("dataset = %s", dataset)
	}
}

func TestWriteCandidateRejectsIncompleteAndSecretValues(t *testing.T) {
	repo := t.TempDir()
	code, err := WriteCandidate(WriteOptions{
		Repo:      repo,
		Candidate: Candidate{EndpointURL: "http://127.0.0.1:9091/v1/analyze"},
		Python:    "validator",
		Run: func(string, []string, []string, string) (CommandResult, error) {
			t.Fatal("incomplete candidate invoked the validator")
			return CommandResult{}, nil
		},
		Stdout: io.Discard,
		Stderr: io.Discard,
	})
	if code != 2 || err == nil || !strings.Contains(err.Error(), "response_path") {
		t.Fatalf("code=%d err=%v", code, err)
	}
	assertNoEvalFiles(t, repo)

	secret := filepath.Join(t.TempDir(), "candidate.json")
	if err := os.WriteFile(secret, []byte(`{"endpoint_url":"http://127.0.0.1:8080/chat","method":"POST","request_field":"text","response_path":"output.text","token":"pat.secret"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCandidateFile(secret); err == nil || err.Error() != "candidate.[redacted]: unknown_field: field is not accepted" || strings.Contains(err.Error(), "pat.secret") {
		t.Fatalf("secret file err = %v", err)
	}
	code, err = WriteCandidate(WriteOptions{
		Repo:      repo,
		Candidate: Candidate{EndpointURL: "http://127.0.0.1:8080/chat", Method: "POST", RequestField: "text", ResponsePath: "output.text", AuthHeader: "Bearer pat.secret"},
		Python:    "validator",
		Run: func(string, []string, []string, string) (CommandResult, error) {
			t.Fatal("secret header invoked the validator")
			return CommandResult{}, nil
		},
		Stdout: io.Discard,
		Stderr: io.Discard,
	})
	if code != 2 || err == nil || !strings.Contains(err.Error(), "auth_header") {
		t.Fatalf("code=%d err=%v", code, err)
	}
	assertNoEvalFiles(t, repo)
}

func TestNonTTYDoesNotReadStdin(t *testing.T) {
	repo := copyFixture(t, "no-agent")
	code, err := Setup(SetupOptions{Repo: repo, Stdout: io.Discard, Stderr: io.Discard})
	if code != 0 || err != nil {
		t.Fatalf("code=%d err=%v", code, err)
	}
	assertNoEvalFiles(t, repo)
}

func TestJaxRSStagesWithoutEndToEndFixture(t *testing.T) {
	jax := inferFixture(t, "java-jaxrs")
	if jax.Language != "java" {
		t.Fatalf("language %s", jax.Language)
	}
	requireHigh(t, jax, "endpoint_path", "/api/v1/rc/evals/rca/analyze")
	requireHigh(t, jax, "method", "POST")
	requireHigh(t, jax, "port", "8080")
	requireHigh(t, jax, "response_path", "path")
	requireHigh(t, jax, "agent_id", "rca")
	if ev, ok := jax.ByField("auth_env"); ok && ev.Value != "" {
		t.Fatalf("auth env invented: %+v", ev)
	}
	if q := FormatLowQuestion(jax); strings.Contains(q, "TR_RCA") || strings.Contains(q, "x-api-key") || strings.Contains(q, "auth_env") {
		t.Fatalf("question = %s", q)
	}
	files := ApplyFiles(jax, false, nil)
	if files == nil {
		t.Fatal("jax-rs route was not staged")
	}
	agentYAML := files["agents/rca/agent.yaml"]
	if !strings.Contains(agentYAML, `method: "POST"`) || !strings.Contains(agentYAML, "http://127.0.0.1:8080/api/v1/rc/evals/rca/analyze") {
		t.Fatalf("agent yaml:\n%s", agentYAML)
	}
	if strings.Contains(agentYAML, "Authorization:") || strings.Contains(agentYAML, "x-api-key:") || strings.Contains(agentYAML, "harness_secret") {
		t.Fatalf("auth header:\n%s", agentYAML)
	}
	dataset := files["datasets/rca.jsonl"]
	for _, field := range []string{"mode", "query", "scope", "context", "toolResults"} {
		if !strings.Contains(dataset, `"`+field+`"`) {
			t.Fatalf("dataset missing %s: %s", field, dataset)
		}
	}
	if !json.Valid([]byte(strings.TrimSpace(dataset))) || strings.Contains(dataset, `"expected":"ok"`) {
		t.Fatalf("dataset = %s", dataset)
	}
	joined := strings.Join(values(files), "\n")
	if strings.Contains(joined, "TR_RCA_EVALUATION_API") {
		t.Fatal("feature flag was staged into the tree")
	}

	repo := copyFixture(t, "java-jaxrs")
	code, err := Setup(SetupOptions{Repo: repo, Stdout: io.Discard, Stderr: io.Discard})
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	assertNoEvalFiles(t, repo)
}

func TestPortAndPathStringsRequireGuide(t *testing.T) {
	inf := inferFixture(t, "port-path")
	if inf.HasSignal {
		t.Fatalf("unsupported port/path strings became route candidates: %+v", inf)
	}
	files := ApplyFiles(inf, false, nil)
	if files != nil {
		t.Fatalf("unsupported port/path strings staged files=%v", files)
	}
}

func TestNoSignalGuideThenStage(t *testing.T) {
	layout := RunnerLayout{Python: "validator"}
	repo := copyFixture(t, "no-agent")
	report, code, err := Scan(repo, "")
	if err != nil || code != 0 || report.HasSignal {
		t.Fatalf("code=%d err=%v report=%+v", code, err, report)
	}
	assertNoEvalFiles(t, repo)
	var stderr bytes.Buffer
	code, err = WriteCandidate(WriteOptions{
		Repo: repo,
		Candidate: Candidate{
			EndpointURL:  "http://localhost:8080/api/chat",
			Method:       "POST",
			RequestField: "query",
			ResponsePath: "reasoning",
			AuthHeader:   "none",
			Cases:        validSampleCases(),
		},
		Confirm: true,
		Python:  layout.Python,
		Stdout:  io.Discard,
		Stderr:  &stderr,
	})
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v stderr=%s", code, err, stderr.String())
	}
	matches, globErr := filepath.Glob(filepath.Join(repo, ".harness", "evals", "agents", "*", "agent.yaml"))
	if globErr != nil || len(matches) != 1 {
		t.Fatalf("matches=%v err=%v", matches, globErr)
	}
	body, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if strings.Contains(text, "onboard-guide-draft") || !strings.Contains(text, "onboarding/v1") || !strings.Contains(text, "POST") || !strings.Contains(text, "reasoning") || !strings.Contains(text, "/api/chat") {
		t.Fatalf("final agent:\n%s", text)
	}
	if strings.Contains(text, "Authorization:") {
		t.Fatalf("none still staged a header:\n%s", text)
	}
}

func TestArchiveDeterministicSymlinkAndLimits(t *testing.T) {
	repo := t.TempDir()
	evals := filepath.Join(repo, ".harness", "evals", "datasets")
	if err := os.MkdirAll(evals, 0o755); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(evals, "support.jsonl")
	body := []byte("{\"id\":\"case-1\"}\n")
	if err := os.WriteFile(real, body, 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(repo, ".harness", "evals", "agents")
	if err := os.MkdirAll(link, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(link, "agent.yaml")); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(real, old, old); err != nil {
		t.Fatal(err)
	}
	first, err := BuildArchive(repo, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(real, old.Add(time.Hour), old.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	second, err := BuildArchive(repo, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("zip bytes changed after a timestamp change")
	}
	reader, err := zip.NewReader(bytes.NewReader(first), int64(len(first)))
	if err != nil {
		t.Fatal(err)
	}
	if len(reader.File) != 1 || reader.File[0].Name != ".harness/evals/datasets/support.jsonl" {
		t.Fatalf("members = %#v", names(reader))
	}
	if reader.File[0].Modified.Year() != 1980 {
		t.Fatalf("timestamp = %s", reader.File[0].Modified)
	}

	limits := DefaultLimits()
	cases := []struct {
		name       string
		members    int
		size       int64
		compressed int
		wantErr    bool
	}{
		{"members minus one", MaxMembers - 1, 1, 1, false},
		{"members plus one", MaxMembers + 1, 1, 1, true},
		{"file minus one", 1, MaxMemberBytes - 1, 1, false},
		{"file plus one", 1, MaxMemberBytes + 1, 1, true},
		{"compressed minus one", 1, 1, MaxCompressedBytes - 1, false},
		{"compressed plus one", 1, 1, MaxCompressedBytes + 1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckClientLimits(limits, tc.members, []int64{tc.size}, tc.compressed)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v", err)
			}
		})
	}
	expanded := make([]int64, 6)
	for i := range expanded {
		expanded[i] = MaxMemberBytes
	}
	if err := CheckClientLimits(limits, len(expanded), expanded, 1); err != nil {
		t.Fatalf("client rejected expanded bytes or compression ratio: %v", err)
	}
}

func TestRunnerBootstrapAndOnboardArgs(t *testing.T) {
	home := t.TempDir()
	err := ensureMissing(home)
	if err == nil || !strings.Contains(err.Error(), "Python was not found on PATH") || !strings.Contains(err.Error(), "uv venv "+home) || !strings.Contains(err.Error(), RunnerPackage+"=="+RunnerVersion) || !strings.Contains(err.Error(), "pipx install") {
		t.Fatalf("clean machine remediation = %v", err)
	}

	var uv int
	install := func(name string, args []string, env []string, dir string) (CommandResult, error) {
		if name == "uv" {
			uv++
			if args[0] == "venv" {
				touch(t, filepath.Join(args[1], "bin", "python"))
				touch(t, filepath.Join(args[1], "bin", "ai-evals"))
			}
			return CommandResult{}, nil
		}
		if filepath.Base(name) == "python" {
			return CommandResult{Stdout: RunnerVersion + "\n"}, nil
		}
		return CommandResult{}, nil
	}
	look := func(bin string) (string, error) {
		if bin == "uv" {
			return "/usr/bin/uv", nil
		}
		return "", os.ErrNotExist
	}
	if _, err := EnsureRunner(home, look, install); err != nil {
		t.Fatal(err)
	}
	if uv != 2 {
		t.Fatalf("uv calls = %d", uv)
	}
	if _, err := EnsureRunner(home, look, install); err != nil {
		t.Fatal(err)
	}
	if uv != 2 {
		t.Fatalf("runner was installed again, uv calls = %d", uv)
	}

	wrong := t.TempDir()
	touch(t, filepath.Join(wrong, "bin", "python"))
	touch(t, filepath.Join(wrong, "bin", "ai-evals"))
	probe := func(name string, args []string, env []string, dir string) (CommandResult, error) {
		if name == "uv" || name == "pipx" {
			t.Fatal("mismatched pin invoked an installer")
		}
		return CommandResult{Stdout: "0.7.6\n"}, nil
	}
	err = func() error {
		_, e := EnsureRunner(wrong, look, probe)
		return e
	}()
	if err == nil || !strings.Contains(err.Error(), "will not upgrade") || !strings.Contains(err.Error(), RunnerVersion) {
		t.Fatalf("mismatch = %v", err)
	}

	var seen []string
	var seenEnv []string
	run := func(name string, args []string, env []string, dir string) (CommandResult, error) {
		if filepath.Base(name) == "python" {
			return CommandResult{Stdout: RunnerVersion + "\n"}, nil
		}
		seen = append(seen, strings.Join(append([]string{name}, args...), " "))
		seenEnv = env
		return CommandResult{}, nil
	}
	root := filepath.Join(t.TempDir(), ".harness", "evals")
	code, err := RunAgent(home, root, "support", "", patAuth(), io.Discard, run)
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	want := strings.Join(OnboardArgs(filepath.Join(home, "bin", "ai-evals"), root, "support", "local"), " ")
	if !containsString(seen, want) {
		t.Fatalf("commands = %v\nwant %s", seen, want)
	}
	if !containsString(seenEnv, "HARNESS_API_KEY=pat.acct.id.secret") || !containsString(seenEnv, "HARNESS_ORG_ID=myorg") || !containsString(seenEnv, "HARNESS_PROJECT_ID=myproj") {
		t.Fatalf("env = %v", seenEnv)
	}

	var ssoEnv []string
	var notice bytes.Buffer
	ssoRun := func(name string, args []string, env []string, dir string) (CommandResult, error) {
		if filepath.Base(name) == "python" {
			return CommandResult{Stdout: RunnerVersion + "\n"}, nil
		}
		if len(args) > 0 && args[0] == OnboardCommand && (len(args) < 2 || args[1] != "--help") {
			ssoEnv = env
		}
		return CommandResult{}, nil
	}
	if _, err := RunAgent(home, root, "support", "local", ssoAuth(), &notice, ssoRun); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(notice.String(), SSORunnerNotice) {
		t.Fatalf("notice = %s", notice.String())
	}
	if strings.Contains(strings.Join(ssoEnv, "\n"), "sso-token") || strings.Contains(strings.Join(ssoEnv, "\n"), "HARNESS_API_KEY=") {
		t.Fatalf("SSO token was forwarded: %v", ssoEnv)
	}
	satEnv, satNotice := RunnerProcessEnv(satAuth())
	if satNotice != "" || !containsString(satEnv, "HARNESS_API_KEY=sat.acct.id.secret") {
		t.Fatalf("sat env=%v notice=%s", satEnv, satNotice)
	}
}

func TestUploadAuthScopeAndURLs(t *testing.T) {
	payload := []byte("zip-bytes")
	pat := patAuth()
	req, err := NewArchiveRequest(pat, payload)
	if err != nil {
		t.Fatal(err)
	}
	if req.Header.Get("x-api-key") != pat.PATToken || req.Header.Get("Authorization") != "" {
		t.Fatalf("pat headers = %#v", req.Header)
	}
	if req.Header.Get("Harness-Account") != "acct" {
		t.Fatalf("account header = %s", req.Header.Get("Harness-Account"))
	}
	if !strings.HasPrefix(req.Header.Get("Content-Type"), "multipart/form-data") {
		t.Fatalf("content type = %s", req.Header.Get("Content-Type"))
	}
	if !strings.Contains(req.URL.Path, "/orgs/myorg/projects/myproj/evals/import-archive") {
		t.Fatalf("path = %s", req.URL.Path)
	}
	if req.URL.Query().Get("accountIdentifier") != "acct" {
		t.Fatalf("query = %s", req.URL.RawQuery)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte(`name="archive"`)) || !bytes.Contains(body, payload) {
		t.Fatal("multipart body is missing the archive part")
	}

	satReq, err := NewArchiveRequest(satAuth(), payload)
	if err != nil {
		t.Fatal(err)
	}
	if satReq.Header.Get("x-api-key") != "sat.acct.id.secret" {
		t.Fatalf("sat header = %s", satReq.Header.Get("x-api-key"))
	}
	ssoReq, err := NewArchiveRequest(ssoAuth(), payload)
	if err != nil {
		t.Fatal(err)
	}
	if ssoReq.Header.Get("Authorization") != "Bearer sso-token" || ssoReq.Header.Get("x-api-key") != "" {
		t.Fatalf("sso headers = %#v", ssoReq.Header)
	}

	orgOnly := patAuth()
	scoped := (&cmdctx.Ctx{Auth: orgOnly, Level: "org"}).ScopedAuth()
	if _, err := NewArchiveRequest(scoped, payload); err == nil || !strings.Contains(err.Error(), "project") {
		t.Fatalf("org scope err = %v", err)
	}

	response := []byte(`{"agents":[{"identifier":"support","id":"11111111-1111-1111-1111-111111111111","ui_path":"/account/acct/all/ai-evals/orgs/myorg/projects/myproj/targets/11111111-1111-1111-1111-111111111111"}],"evals":[{"identifier":"support-eval","id":"22222222-2222-2222-2222-222222222222","ui_path":"/account/acct/all/ai-evals/orgs/myorg/projects/myproj/evals/22222222-2222-2222-2222-222222222222"}],"datasets":[],"metric_sets":[],"suites":[]}`)
	text, err := UploadArchive(pat, payload, func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 201, Body: io.NopCloser(bytes.NewReader(response)), Header: make(http.Header)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "Agent support https://app.harness.io/account/acct/all/ai-evals/orgs/myorg/projects/myproj/targets/11111111-1111-1111-1111-111111111111") {
		t.Fatalf("agent url missing:\n%s", text)
	}
	if !strings.Contains(text, "Eval support-eval https://app.harness.io/account/acct/all/ai-evals/orgs/myorg/projects/myproj/evals/22222222-2222-2222-2222-222222222222") {
		t.Fatalf("eval url missing:\n%s", text)
	}
	if !strings.Contains(text, GitSyncRecommendation) {
		t.Fatalf("git sync recommendation missing:\n%s", text)
	}
}

func TestUploadV1TreePostsWithoutRunnerVerification(t *testing.T) {
	repo := t.TempDir()
	code, err := WriteCandidate(WriteOptions{
		Repo:      repo,
		Candidate: Candidate{Wire: onboardingV1FixtureDocument()},
		Confirm:   true,
		Stdout:    io.Discard,
		Stderr:    io.Discard,
	})
	if code != 0 || err != nil {
		t.Fatalf("stage code=%d err=%v", code, err)
	}

	var posted bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posted = true
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read archive: %v", err)
		}
		if !bytes.Contains(body, []byte("schema_version: onboarding/v1")) {
			t.Errorf("archive did not contain the v1 agent document")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"agents":[{"identifier":"support","ui_path":"/agent"}],"evals":[{"identifier":"support-eval","ui_path":"/eval"}]}`)
	}))
	defer server.Close()

	t.Setenv(hbase.EnvCLIHome, t.TempDir())
	auth := patAuth()
	auth.APIUrl = server.URL
	ctx := &cmdctx.Ctx{
		Auth:       auth,
		FlagValues: map[string]any{"root": filepath.Join(repo, ".harness", "evals")},
	}
	if err := uploadWorkflow(ctx); err != nil {
		t.Fatalf("upload returned error: %v", err)
	}
	if !posted {
		t.Fatal("upload did not POST the archive")
	}
}

func TestCrossRepoWorkflowPinsReviewedD2FixtureRevision(t *testing.T) {
	const reviewedD2Ref = "8617179d2a072fd019ad9729dfd2c8ab02096f77"
	workflow, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "onboarding-v1-cross-repo.yml"))
	if err != nil {
		if os.IsNotExist(err) {
			t.Skip("cross-repository compatibility workflow is maintained outside the CLI PR")
		}
		t.Fatal(err)
	}
	workflowText := string(workflow)
	if !strings.Contains(workflowText, "D2_REF: "+reviewedD2Ref) {
		t.Fatalf("workflow does not pin reviewed D2 ref %s", reviewedD2Ref)
	}
	if strings.Contains(workflowText, "223b19869f2d1f012e4ceddc4b11bc7837c08bcf") {
		t.Fatal("workflow still pins the superseded D2 ref")
	}

	docs, err := os.ReadFile(filepath.Join("..", "..", "docs", "onboarding-v1-cross-repo.md"))
	if err != nil {
		t.Fatal(err)
	}
	docsText := string(docs)
	if !strings.Contains(docsText, "D2_REF") || !strings.Contains(docsText, "fixture and reference together") {
		t.Fatalf("docs do not require fixture/reference updates together:\n%s", docsText)
	}
}

func TestStableCommandsRegister(t *testing.T) {
	t.Setenv(hbase.EnvEnabledModules, "aievals")
	t.Setenv(hbase.EnvCLIHome, t.TempDir())
	reg := registry.New()
	if err := specloader.LoadSpecs(reg); err != nil {
		t.Fatal(err)
	}
	ModuleInit(reg.Module("aievals"))
	for _, verb := range []string{"setup", "scan", "stage", "run", "upload"} {
		cs := reg.GetSpec(verb, "eval_onboarding")
		if cs == nil {
			t.Fatalf("missing %s eval_onboarding", verb)
		}
		if cs.WorkflowID != "aievals:"+verb {
			t.Fatalf("%s workflow = %s", verb, cs.WorkflowID)
		}
		if !strings.Contains(cs.Long, "Stable eval_onboarding command") &&
			!strings.Contains(cs.Long, "stable onboarding interface") {
			t.Fatalf("%s is missing stable onboarding wording", verb)
		}
	}
}

func assertNoEvalFiles(t *testing.T, repo string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(repo, ".harness", "evals", "agents", "*", "agent.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("eval files were written: %v", matches)
	}
}

func inferFixture(t *testing.T, name string) Inference {
	t.Helper()
	inf, err := Infer(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return inf
}

func copyFixture(t *testing.T, name string) string {
	t.Helper()
	dest := t.TempDir()
	src := filepath.Join("testdata", name)
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dest
}

func requireHigh(t *testing.T, inf Inference, field, value string) {
	t.Helper()
	requireLevel(t, inf, field, value, ConfidenceHigh)
}

func requireLevel(t *testing.T, inf Inference, field, value string, level Confidence) {
	t.Helper()
	ev, ok := inf.ByField(field)
	if !ok || ev.Value != value || ev.Confidence != level || ev.Source == "" {
		t.Fatalf("%s = %+v, want %s %s", field, ev, value, level)
	}
}

func assertTree(t *testing.T, inf Inference, agent, port, path string, withSecret bool) {
	t.Helper()
	agentYAML := inf.Files["agents/"+agent+"/agent.yaml"]
	if !strings.Contains(agentYAML, `identifier: "`+agent+`"`) || !strings.Contains(agentYAML, `method: "POST"`) || !strings.Contains(agentYAML, `response_path: "output.text"`) {
		t.Fatalf("agent yaml:\n%s", agentYAML)
	}
	if !strings.Contains(agentYAML, `endpoint: "http://127.0.0.1:`+port+path+`"`) {
		t.Fatalf("endpoint missing:\n%s", agentYAML)
	}
	if withSecret && !strings.Contains(agentYAML, "env: SUPPORT_TOKEN") {
		t.Fatalf("secret name missing:\n%s", agentYAML)
	}
	if !withSecret && strings.Contains(agentYAML, "SUPPORT_TOKEN") {
		t.Fatalf("unanswered secret name was written:\n%s", agentYAML)
	}
	dataset := inf.Files["datasets/"+agent+".jsonl"]
	if dataset != "{\"id\":\"case-1\",\"input\":{\"text\":\"hello\"},\"expected\":\"ok\"}\n" {
		t.Fatalf("dataset = %q", dataset)
	}
	metric := inf.Files["metric-sets/"+agent+".yaml"]
	if !strings.Contains(metric, "kind: exact_match") || !strings.Contains(metric, `identifier: "`+agent+`"`) {
		t.Fatalf("metric set:\n%s", metric)
	}
	if strings.Contains(agentYAML, "eval.yaml") {
		t.Fatal("tree contains eval.yaml")
	}
}

func values(files map[string]string) []string {
	out := make([]string, 0, len(files))
	for _, body := range files {
		out = append(out, body)
	}
	return out
}

func names(r *zip.Reader) []string {
	out := make([]string, len(r.File))
	for i, f := range r.File {
		out[i] = f.Name
	}
	return out
}

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFixtureFile(t *testing.T, root, relative, body string) {
	t.Helper()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func ensureMissing(home string) error {
	look := func(string) (string, error) { return "", os.ErrNotExist }
	run := func(name string, args []string, env []string, dir string) (CommandResult, error) {
		return CommandResult{}, os.ErrNotExist
	}
	_, err := EnsureRunner(home, look, run)
	return err
}

func patAuth() *auth.ResolvedAuth {
	return &auth.ResolvedAuth{
		AuthType:  auth.AuthTypePAT,
		TokenKind: auth.TokenKindPAT,
		PATToken:  "pat.acct.id.secret",
		APIUrl:    "https://app.harness.io",
		AccountID: "acct",
		OrgID:     "myorg",
		ProjectID: "myproj",
	}
}

func satAuth() *auth.ResolvedAuth {
	a := patAuth()
	a.TokenKind = auth.TokenKindSAT
	a.PATToken = "sat.acct.id.secret"
	return a
}

func ssoAuth() *auth.ResolvedAuth {
	return &auth.ResolvedAuth{
		AuthType:  auth.AuthTypeSSO,
		TokenKind: auth.TokenKindJWT,
		SSOToken:  "sso-token",
		APIUrl:    "https://app.harness.io",
		UIUrl:     "https://app.harness.io",
		AccountID: "acct",
		OrgID:     "myorg",
		ProjectID: "myproj",
	}
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
