// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

func candidateRoutes(inf Inference) []string {
	out := make([]string, 0, len(inf.RouteCandidates))
	for _, candidate := range inf.RouteCandidates {
		out = append(out, candidate.Route())
	}
	sort.Strings(out)
	return out
}

func requireRoutes(t *testing.T, inf Inference, want ...string) {
	t.Helper()
	sort.Strings(want)
	got := candidateRoutes(inf)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("routes=%q want=%q notes=%q", got, want, inf.Notes)
	}
}

func requireAbsent(t *testing.T, inf Inference, fields ...string) {
	t.Helper()
	for _, field := range fields {
		if ev, ok := inf.ByField(field); ok {
			t.Fatalf("unexpected %s evidence: %+v", field, ev)
		}
	}
}

func inferRepo(t *testing.T, repo, route string) Inference {
	t.Helper()
	inf, err := InferWithRoute(repo, route)
	if err != nil {
		t.Fatal(err)
	}
	return inf
}

func TestASTAdaptersIgnoreCommentsEverywhere(t *testing.T) {
	cases := []struct {
		name, path, body string
	}{
		{
			name: "java",
			path: "src/main/java/example/Controller.java",
			body: `package example;
import org.springframework.web.bind.annotation.*;
/** @PostMapping("/javadoc") */
@RequestMapping(
    // "/line-comment-prefix"
    value = /* "/block-prefix" */ "/api")
class Controller {
  // @PostMapping("/line")
  /* @PostMapping("/block") */
  @PostMapping(
      /* "/inline-comment" */
      "/real")
  Reply real(Request request) { return null; }
}
`,
		},
		{
			name: "python",
			path: "src/app.py",
			body: `from fastapi import FastAPI
app = FastAPI()
# @app.post("/comment")
@app.post(
    # "/inline-comment"
    "/api/real",  # trailing "/fake"
)
def real():
    """@app.post("/docstring")"""
    return {}
`,
		},
		{
			name: "typescript",
			path: "src/server.ts",
			body: `import express from "express"
const app = express()
/* app.post("/block", handler) */
app.post(
  // "/line"
  "/api/real",
  handler,
)
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			writeFixtureFile(t, repo, tc.path, tc.body)
			requireRoutes(t, inferRepo(t, repo, ""), "POST /api/real")
		})
	}
}

func TestASTAdaptersDecodeEveryStringLiteralForm(t *testing.T) {
	cases := []struct {
		name, path, body string
		want             []string
	}{
		{
			name: "java",
			path: "src/main/java/example/Literals.java",
			body: `package example;
import org.springframework.web.bind.annotation.*;
class Literals {
  static final String CONSTANT = "/constant";
  @GetMapping("/plain") Reply plain() { return null; }
  @GetMapping("/con" + "cat") Reply concat() { return null; }
  @GetMapping("/escaped\u0041\u0042") Reply escaped() { return null; }
  @GetMapping("""
      /text-block""") Reply textBlock() { return null; }
  @GetMapping(value = {"/array-one", "/array-two"}) Reply array() { return null; }
  @GetMapping(CONSTANT) Reply constant() { return null; }
  @GetMapping(CONSTANT + "/suffix") Reply constantSuffix() { return null; }
}
`,
			want: []string{"GET /plain", "GET /concat", "GET /escapedAB", "GET /text-block", "GET /array-one", "GET /array-two"},
		},
		{
			name: "python",
			path: "src/app.py",
			body: `from fastapi import FastAPI
app = FastAPI()
prefix = "/p"
@app.get('/single')
def a(): return {}
@app.get("/double")
def b(): return {}
@app.get('''/triple-single''')
def c(): return {}
@app.get("""/triple-double""")
def d(): return {}
@app.get(r"/raw\d")
def e(): return {}
@app.get("/con" 'cat')
def f(): return {}
@app.get(u"/unicode")
def g(): return {}
@app.get(f"/fstatic")
def h(): return {}
@app.get("/esc\x41")
def i(): return {}
@app.get(f"{prefix}/dynamic")
def j(): return {}
@app.get(b"/bytes")
def k(): return {}
@app.get(prefix)
def l(): return {}
`,
			want: []string{"GET /single", "GET /double", "GET /triple-single", "GET /triple-double", `GET /raw\d`, "GET /concat", "GET /unicode", "GET /fstatic", "GET /escA"},
		},
		{
			name: "typescript",
			path: "src/server.ts",
			body: "import express from \"express\"\n" +
				"const app = express()\n" +
				"const prefix = \"/p\"\n" +
				"app.get('/single', h)\n" +
				"app.get(\"/double\", h)\n" +
				"app.get(`/template`, h)\n" +
				"app.get(\"/con\" + 'cat', h)\n" +
				"app.get(\"/esc\\u0041\\x42\", h)\n" +
				"app.get(`${prefix}/dynamic`, h)\n" +
				"app.get(prefix, h)\n",
			want: []string{"GET /single", "GET /double", "GET /template", "GET /concat", "GET /escAB"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			writeFixtureFile(t, repo, tc.path, tc.body)
			requireRoutes(t, inferRepo(t, repo, ""), tc.want...)
		})
	}
}

func TestSpringRequestMappingASTVariants(t *testing.T) {
	repo := t.TempDir()
	writeFixtureFile(t, repo, "src/main/java/example/Variants.java", `package example;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestMethod;
import static org.springframework.web.bind.annotation.RequestMethod.PATCH;

@RequestMapping(path = {"/v1", "/v2"})
class Variants {
  @RequestMapping(
      value = "/multi-line",
      method = RequestMethod.POST,
      produces = "application/json")
  Reply multiLine(Request request) { return null; }

  @RequestMapping(path = "/static-import", method = PATCH)
  Reply staticImport(Request request) { return null; }

  @RequestMapping(path = "/no-method")
  Reply noMethod() { return null; }

  @org.springframework.web.bind.annotation.PutMapping("/qualified")
  Reply qualified(Request request) { return null; }

  @GetMapping
  Reply root() { return null; }

  @RequestMapping(path = "/conflict", value = "/other", method = RequestMethod.DELETE)
  Reply conflict() { return null; }
}

@RequestMapping(value = "/inherited", method = RequestMethod.POST)
class Inherited {
  @RequestMapping("/child")
  Reply child(Request request) { return null; }
}
`)
	inf := inferRepo(t, repo, "")
	requireRoutes(t, inf,
		"POST /v1/multi-line", "POST /v2/multi-line",
		"PATCH /v1/static-import", "PATCH /v2/static-import",
		"PUT /v1/qualified", "PUT /v2/qualified",
		"GET /v1", "GET /v2",
		"POST /inherited/child",
	)
	notes := strings.Join(inf.Notes, "\n")
	for _, want := range []string{"/no-method", "/conflict"} {
		if !strings.Contains(notes, want) {
			t.Fatalf("unsupported mapping %s was not reported: %q", want, inf.Notes)
		}
	}
}

func TestMultiServiceDuplicateRouteAndDTOStayServiceScoped(t *testing.T) {
	repo := t.TempDir()
	for _, service := range []string{"catalog", "billing"} {
		root := "services/" + service
		writeFixtureFile(t, repo, root+"/src/main/java/com/example/api/ReportController.java", `package com.example.api;
import org.springframework.web.bind.annotation.*;
@RestController
class ReportController {
  @PostMapping("/reports")
  ReportResponse reports(@RequestHeader("Authorization") String auth, @RequestBody ReportRequest body) { return null; }
}
`)
		writeFixtureFile(t, repo, root+"/src/main/java/com/example/api/ReportRequest.java", fmt.Sprintf(`package com.example.api;
public record ReportRequest(String %sQuery, int %sLimit) {}
`, service, service))
		writeFixtureFile(t, repo, root+"/src/main/java/com/example/api/ReportResponse.java", fmt.Sprintf(`package com.example.api;
public class ReportResponse { private String %sResult; }
`, service))
		writeFixtureFile(t, repo, root+"/src/test/java/com/example/api/ReportControllerTest.java", fmt.Sprintf(`package com.example.api;
import org.springframework.test.web.servlet.MockMvc;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;
class ReportControllerTest {
  MockMvc mockMvc;
  void reports() {
    mockMvc.perform(post("/reports").header("Authorization", System.getenv("%s_TOKEN")).content("{\"%sQuery\":\"%s-input\"}"))
        .andExpect(jsonPath("$.%sResult").value("%s-result"));
  }
}
`, strings.ToUpper(service), service, service, service, service))
	}

	all := inferRepo(t, repo, "")
	if len(all.RouteCandidates) != 2 || all.RouteAmbiguity() == nil {
		t.Fatalf("duplicate service routes = %+v", all.RouteCandidates)
	}
	unqualified := inferRepo(t, repo, "POST /reports")
	if unqualified.Files != nil {
		t.Fatalf("unqualified duplicate route staged files: %v", unqualified.Files)
	}

	for _, tc := range []struct{ service, other string }{{"billing", "catalog"}, {"catalog", "billing"}} {
		t.Run(tc.service, func(t *testing.T) {
			inf := inferRepo(t, repo, "POST /reports [services/"+tc.service+"]")
			requireHigh(t, inf, "agent_id", tc.service)
			requireHigh(t, inf, "request_fields", tc.service+"Query,"+tc.service+"Limit")
			requireHigh(t, inf, "request_field", tc.service+"Query")
			requireHigh(t, inf, "response_path", tc.service+"Result")
			requireHigh(t, inf, "auth_env", strings.ToUpper(tc.service)+"_TOKEN")
			requireHigh(t, inf, "test_input_text", tc.service+"-input")
			requireHigh(t, inf, "test_expected", tc.service+"-result")
			if leaked := fmt.Sprintf("%+v", inf.Evidence); strings.Contains(strings.ToLower(leaked), tc.other) {
				t.Fatalf("%s inherited %s evidence: %s", tc.service, tc.other, leaked)
			}
			if inf.Files == nil {
				t.Fatal("service-qualified route did not stage")
			}
		})
	}

	py := t.TempDir()
	for _, service := range []string{"alpha", "beta"} {
		writeFixtureFile(t, py, "services/"+service+"/app.py", fmt.Sprintf(`from fastapi import FastAPI
from pydantic import BaseModel
app = FastAPI()
class ReportRequest(BaseModel):
    %s_text: str
class ReportResponse(BaseModel):
    %s_answer: str
@app.post("/reports")
def reports(body: ReportRequest) -> ReportResponse:
    return ReportResponse()
`, service, service))
	}
	beta := inferRepo(t, py, "POST /reports [services/beta]")
	requireHigh(t, beta, "request_fields", "beta_text")
	requireHigh(t, beta, "response_path", "beta_answer")
}

func TestSameTestMultiCallEvidenceBindsEachCall(t *testing.T) {
	cases := []struct {
		name                         string
		sourcePath, source           string
		testPath, test               string
		getExpected, postEnv, postIn string
		postExpected                 string
	}{
		{
			name:       "java",
			sourcePath: "src/main/java/example/RouteController.java",
			source: `package example;
import org.springframework.web.bind.annotation.*;
class RouteController {
  @GetMapping("/reports") Reply list() { return null; }
  @PostMapping("/reports") Reply create(@RequestHeader("Authorization") String auth, @RequestBody Request body) { return null; }
}
record Request(String text) {}
record Reply(String text) {}
`,
			testPath: "src/test/java/example/RouteControllerTest.java",
			test: `package example;
import org.springframework.test.web.servlet.MockMvc;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;
class RouteControllerTest {
  MockMvc mockMvc;
  void both() {
    String token = System.getenv("POST_TOKEN");
    mockMvc.perform(get("/reports")).andExpect(jsonPath("$.text").value("get-result"));
    mockMvc.perform(post("/reports")
        .header("Authorization", token)
        .content("{\"text\":\"post-input\"}"))
        .andExpect(jsonPath("$.text").value("post-result"));
  }
}
`,
		},
		{
			name:       "python",
			sourcePath: "service/app.py",
			source: `from fastapi import FastAPI
app = FastAPI()
@app.get("/reports")
def list_reports(): return {}
@app.post("/reports")
def create_report(): return {}
`,
			testPath: "service/tests/test_reports.py",
			test: `import os
from fastapi.testclient import TestClient
from service.app import app
client = TestClient(app)
def test_both():
    response = client.get("/reports")
    status = response.status_code
    assert response.json()["text"] == "get-result"
    token = os.getenv("POST_TOKEN")
    response = client.post(
        "/reports",
        headers={"Authorization": token},
        json={"text": "post-input"},
    )
    assert status == 200
    assert response.json()["text"] == "post-result"
`,
		},
		{
			name:       "typescript",
			sourcePath: "service/server.ts",
			source: `import express from "express"
const app = express()
app.get("/reports", handler)
app.post("/reports", handler)
export default app
`,
			testPath: "service/server.test.ts",
			test: `import request from "supertest"
import app from "./server"
test("both", async () => {
  const token = process.env.POST_TOKEN
  await request(app).get("/reports").expect({ text: "get-result" })
  await request(app)
    .post("/reports")
    .set("Authorization", token)
    .send({ text: "post-input" })
    .expect({ text: "post-result" })
})
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			writeFixtureFile(t, repo, tc.sourcePath, tc.source)
			writeFixtureFile(t, repo, tc.testPath, tc.test)

			get := inferRepo(t, repo, "GET /reports")
			requireHigh(t, get, "test_expected", "get-result")
			requireAbsent(t, get, "auth_env", "test_input_text")

			post := inferRepo(t, repo, "POST /reports")
			requireHigh(t, post, "auth_header", "Authorization")
			requireHigh(t, post, "auth_env", "POST_TOKEN")
			requireHigh(t, post, "test_input_text", "post-input")
			requireHigh(t, post, "test_expected", "post-result")
			if strings.Contains(fmt.Sprintf("%+v", post.Evidence), "get-result") {
				t.Fatalf("POST inherited GET evidence: %+v", post.Evidence)
			}
		})
	}
}

func TestTestEvidenceRequiresProvenFrameworkClients(t *testing.T) {
	cases := []struct {
		name, sourcePath, source, testPath, test string
	}{
		{
			name:       "java",
			sourcePath: "src/main/java/example/RouteController.java",
			source: `package example;
import org.springframework.web.bind.annotation.*;
class RouteController {
  @PostMapping("/reports") Reply create(@RequestHeader("Authorization") String auth, @RequestBody Request body) { return null; }
}
record Request(String text) {}
record Reply(String text) {}
`,
			testPath: "src/test/java/example/RouteControllerTest.java",
			test: `package example;
class RouteControllerTest {
  void reports() {
    mystery.perform(post("/reports").header("Authorization", System.getenv("GENERIC_TOKEN")).content("{\"text\":\"generic-input\"}"))
      .andExpect(jsonPath("$.text").value("generic-result"));
  }
}
`,
		},
		{
			name:       "python",
			sourcePath: "service/app.py",
			source: `from fastapi import FastAPI
app = FastAPI()
@app.post("/reports")
def create_report(): return {}
`,
			testPath: "service/tests/test_reports.py",
			test: `def test_report():
    response = client.post("/reports", headers={"Authorization": "generic"}, json={"text": "generic-input"})
    assert response.json()["text"] == "generic-result"
`,
		},
		{
			name:       "typescript",
			sourcePath: "service/server.ts",
			source: `import express from "express"
const app = express()
app.post("/reports", handler)
`,
			testPath: "service/server.test.ts",
			test: `test("reports", async () => {
  await x.post("/reports").set("Authorization", process.env.GENERIC_TOKEN).send({ text: "generic-input" }).expect({ text: "generic-result" })
})
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			writeFixtureFile(t, repo, tc.sourcePath, tc.source)
			writeFixtureFile(t, repo, tc.testPath, tc.test)
			inf := inferRepo(t, repo, "POST /reports")
			requireAbsent(t, inf, "auth_env", "test_input_text", "test_expected")
		})
	}
}

func TestTypedRouteParametersAcrossAdapters(t *testing.T) {
	t.Run("spring", func(t *testing.T) {
		repo := t.TempDir()
		writeFixtureFile(t, repo, "src/main/java/com/example/typed/TypedController.java", `package com.example.typed;
import com.fasterxml.jackson.annotation.JsonProperty;
import jakarta.validation.Valid;
import java.util.*;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.*;

@RestController
@RequestMapping("/typed")
public class TypedController {
  @PostMapping("/{id}/items/{slot}")
  public ResponseEntity<TypedResponse> create(
      @PathVariable("id") final UUID id,
      @PathVariable final int slot,
      @RequestParam(value = "tags") List<String> tags,
      @RequestParam(name = "mode", required = false) Optional<String> mode,
      @RequestParam(defaultValue = "10") int limit,
      @RequestHeader("x-api-key") String key,
      @Valid @RequestBody TypedRequest body) {
    return null;
  }
}

record TypedRequest(@JsonProperty("prompt_text") String prompt, Map<String, String> metadata) {}
record TypedResponse(Map<String, Object> meta, Inner inner) {
  record Inner(String answer) {}
}
`)
		inf := inferRepo(t, repo, "")
		requireHigh(t, inf, "endpoint_path", "/typed/{id}/items/{slot}")
		requireHigh(t, inf, "required_path_params", "id,slot")
		requireHigh(t, inf, "path_param_mappings", "id:id,slot:slot")
		requireHigh(t, inf, "required_query_params", "tags")
		requireHigh(t, inf, "query_param_mappings", "tags:tags")
		requireHigh(t, inf, "auth_header", "x-api-key")
		requireHigh(t, inf, "request_fields", "prompt_text,metadata")
		requireHigh(t, inf, "request_field", "prompt_text")
		requireHigh(t, inf, "response_path", "inner.answer")
	})

	t.Run("fastapi", func(t *testing.T) {
		repo := t.TempDir()
		writeFixtureFile(t, repo, "service/app.py", `from typing import Annotated, Optional
from fastapi import FastAPI, Header, Query
from pydantic import BaseModel, Field

app = FastAPI()

class TypedRequest(BaseModel):
    prompt: str = Field(alias="prompt_text")
    count: int = 1

class Inner(BaseModel):
    answer: str

class TypedResponse(BaseModel):
    score: float
    inner: Inner

@app.post("/typed/{item_id}", response_model=TypedResponse)
async def typed(
    item_id: int,
    body: TypedRequest,
    search: Annotated[str, Query(alias="q")],
    limit: int = 10,
    mode: Optional[str] = None,
    x_api_key: Annotated[str, Header()] = "",
):
    return {}
`)
		inf := inferRepo(t, repo, "")
		requireHigh(t, inf, "required_path_params", "item_id")
		requireHigh(t, inf, "required_query_params", "q")
		requireHigh(t, inf, "query_param_mappings", "q:search")
		requireHigh(t, inf, "auth_header", "x-api-key")
		requireHigh(t, inf, "request_fields", "prompt_text,count")
		requireHigh(t, inf, "request_field", "prompt_text")
		requireHigh(t, inf, "response_path", "inner.answer")
	})

	t.Run("express", func(t *testing.T) {
		repo := t.TempDir()
		writeFixtureFile(t, repo, "service/server.ts", `import express, { Request, Response } from "express"

interface TypedRequest {
  prompt: string
  count?: number
}
type TypedResponse = { score: number; inner: { answer: string } }

const app = express()
app.post("/typed/:itemId/:slot", (req: Request<{ itemId: string; slot: string }, TypedResponse, TypedRequest>, res: Response<TypedResponse>) => {})
`)
		inf := inferRepo(t, repo, "")
		requireHigh(t, inf, "required_path_params", "itemId,slot")
		requireHigh(t, inf, "request_fields", "prompt,count")
		requireHigh(t, inf, "request_field", "prompt")
		requireHigh(t, inf, "response_path", "inner.answer")
	})
}

func TestDTOAndParameterExtractionSkipsNonPayloadMembers(t *testing.T) {
	repo := t.TempDir()
	writeFixtureFile(t, repo, "services/java/src/main/java/example/Controller.java", `package example;
import com.fasterxml.jackson.annotation.JsonIgnore;
import org.springframework.web.bind.annotation.*;
class Controller {
  @PostMapping("/java") Reply post(@RequestBody Payload payload) { return null; }
}
class Payload {
  private static final long serialVersionUID = 1L;
  @JsonIgnore private String internalNote;
  private String prompt;
  private int count;
}
`)
	writeFixtureFile(t, repo, "services/py/app.py", `from fastapi import FastAPI
from starlette.requests import Request
app = FastAPI()
@app.post("/py")
def post(request: Request, q: str): return {}
`)
	java := inferRepo(t, repo, "POST /java")
	requireHigh(t, java, "request_fields", "prompt,count")
	py := inferRepo(t, repo, "POST /py")
	requireHigh(t, py, "required_query_params", "q")
}

func TestStrictAdaptersRefuseUnprovenRoutes(t *testing.T) {
	repo := t.TempDir()
	writeFixtureFile(t, repo, "services/py/routes.py", `from fastapi import APIRouter
router = APIRouter()
@router.post("/unmounted")
def unmounted(): return {}
`)
	writeFixtureFile(t, repo, "services/js/routes.ts", `import express from "express"
const router = express.Router()
router.post("/unmounted", handler)
`)
	writeFixtureFile(t, repo, "services/broken/src/main/java/example/Broken.java", `package example;
import org.springframework.web.bind.annotation.*;
class Broken {
  @PostMapping("/broken")
  Reply broken( { return null; }
}
`)
	writeFixtureFile(t, repo, "services/mixed/src/main/java/example/Mixed.java", `package example;
import org.springframework.web.bind.annotation.PostMapping;
import javax.ws.rs.POST;
import javax.ws.rs.Path;
class Mixed {
  @PostMapping("/spring")
  @POST
  @Path("/jaxrs")
  Reply mixed() { return null; }
}
`)
	inf := inferRepo(t, repo, "")
	if len(inf.RouteCandidates) != 0 || inf.HasSignal || ApplyFiles(inf, true, nil) != nil {
		t.Fatalf("unproven routes became candidates: %+v", inf.RouteCandidates)
	}
	notes := strings.Join(inf.Notes, "\n")
	for _, want := range []string{"services/py/routes.py", "services/js/routes.ts", "services/broken/src/main/java/example/Broken.java", "services/mixed/src/main/java/example/Mixed.java"} {
		if !strings.Contains(notes, want) {
			t.Fatalf("skipped source %s was not reported: %q", want, inf.Notes)
		}
	}
}

func TestRouterMountsComposeASTPrefixes(t *testing.T) {
	repo := t.TempDir()
	writeFixtureFile(t, repo, "services/py/app.py", `from fastapi import FastAPI, APIRouter
app = FastAPI()
router = APIRouter(prefix="/r")
@router.post("/items")
def items(): return {}
app.include_router(router, prefix="/api")
`)
	writeFixtureFile(t, repo, "services/js/server.js", `const express = require("express")
const app = express()
const router = express.Router()
router.post("/items", handler)
app.use("/api", router)
`)
	requireRoutes(t, inferRepo(t, repo, ""), "POST /api/r/items", "POST /api/items")
}

func TestPortEvidenceRequiresProvenServerCalls(t *testing.T) {
	cases := []struct {
		name, path, body string
		extraPath, extra string
		want             string
	}{
		{
			name: "express const",
			path: "service/server.ts",
			body: `import express from "express"
const PORT = 4010
const app = express()
app.post("/reports", handler)
app.listen(PORT)
`,
			want: "4010",
		},
		{
			name: "express env fallback is not proven",
			path: "service/server.ts",
			body: `import express from "express"
const app = express()
app.post("/reports", handler)
app.listen(process.env.PORT || 4020)
`,
			want: "8080",
		},
		{
			name: "uvicorn dynamic port is not proven",
			path: "service/app.py",
			body: `import os
import uvicorn
from fastapi import FastAPI
app = FastAPI()
@app.post("/reports")
def reports(): return {}
uvicorn.run(app, port=int(os.environ["PORT"]))
`,
			want: "8080",
		},
		{
			name: "spring properties and yaml are not executable port evidence",
			path: "src/main/java/example/Controller.java",
			body: `package example;
import org.springframework.web.bind.annotation.*;
class Controller {
  @PostMapping("/reports") Reply reports() { return null; }
}
`,
			extraPath: "src/main/resources/application.yml",
			extra:     "server:\n  port: 8099\n",
			want:      "8080",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			writeFixtureFile(t, repo, tc.path, tc.body)
			if tc.extraPath != "" {
				writeFixtureFile(t, repo, tc.extraPath, tc.extra)
			}
			inf := inferRepo(t, repo, "")
			requireHigh(t, inf, "port", tc.want)
			if tc.extraPath != "" && !strings.Contains(strings.Join(inf.Notes, "\n"), "No AST-proven executable server port call") {
				t.Fatalf("configuration-only port needs actionable note: %q", inf.Notes)
			}
		})
	}
}
