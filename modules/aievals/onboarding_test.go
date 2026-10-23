// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
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
	for _, rule := range SkillRules() {
		if !strings.Contains(text, rule) {
			t.Errorf("skill drifted from rule %q", rule)
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
	if !strings.Contains(dest, filepath.Join(".cursor", "skills", "aievals-onboarding", "SKILL.md")) {
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

	ts := inferFixture(t, "typescript-support")
	if ts.Language != "typescript" {
		t.Fatalf("language %s", ts.Language)
	}
	requireHigh(t, ts, "endpoint_path", "/chat")
	requireHigh(t, ts, "port", "3000")
	requireHigh(t, ts, "response_path", "output.text")
	requireHigh(t, ts, "auth_env", "SUPPORT_TOKEN")
	assertTree(t, ts, "support", "3000", "/chat", true)

	py := inferFixture(t, "python-support")
	if py.Language != "python" {
		t.Fatalf("language %s", py.Language)
	}
	requireHigh(t, py, "endpoint_path", "/chat")
	requireHigh(t, py, "port", "8000")
	requireHigh(t, py, "response_path", "output.text")
	requireHigh(t, py, "auth_env", "SUPPORT_TOKEN")
	assertTree(t, py, "support", "8000", "/chat", true)

	untyped := inferFixture(t, "untyped-support")
	if untyped.Language != "untyped" {
		t.Fatalf("language %s", untyped.Language)
	}
	requireLevel(t, untyped, "endpoint_path", "/chat", ConfidenceMedium)
	requireLevel(t, untyped, "method", "POST", ConfidenceMedium)
	requireLevel(t, untyped, "response_path", "output.text", ConfidenceMedium)
	env, ok := untyped.ByField("auth_env")
	if !ok || env.Confidence != ConfidenceLow || env.Value != "" {
		t.Fatalf("auth_env = %+v", env)
	}
	if len(untyped.Medium()) == 0 || len(untyped.Low()) == 0 {
		t.Fatalf("expected one medium batch and one low gap, medium=%d low=%d", len(untyped.Medium()), len(untyped.Low()))
	}
	if !strings.Contains(FormatMediumConfirmation(untyped), "Confirm these medium-confidence values:") {
		t.Fatal("medium batch missing")
	}
	if !strings.Contains(FormatLowQuestion(untyped), "One question:") || strings.Contains(FormatLowQuestion(untyped), "literal-secret") {
		t.Fatalf("low question = %s", FormatLowQuestion(untyped))
	}
	for _, body := range untyped.Files {
		if strings.Contains(body, "literal-secret") {
			t.Fatalf("candidate tree contains a secret value:\n%s", body)
		}
	}
	assertTree(t, untyped, "support", "4000", "/chat", false)
	answered := ApplyFiles(untyped, true, map[string]string{"auth_env": "SUPPORT_TOKEN"})
	joined := strings.Join(values(answered), "\n")
	if !strings.Contains(joined, "env: SUPPORT_TOKEN") || strings.Contains(joined, "literal-secret") {
		t.Fatalf("answered tree:\n%s", joined)
	}
	if ApplyFiles(untyped, false, nil) != nil {
		t.Fatal("untyped medium values were auto-staged")
	}
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
	if got[0] != "python-test" || got[1] != "-c" || got[2] != ValidatorSnippet || !strings.HasSuffix(got[3], filepath.Join("repo", ".harness", "evals")) {
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

func TestNonTTYDoesNotReadStdin(t *testing.T) {
	repo := copyFixture(t, "untyped-support")
	var stderr bytes.Buffer
	code, err := Setup(SetupOptions{
		Repo:          repo,
		IsPty:         false,
		Stdout:        &bytes.Buffer{},
		Stderr:        &stderr,
		SkipValidator: true,
		ReadLine: func() (string, error) {
			t.Fatal("non-TTY setup read stdin")
			return "", nil
		},
	})
	if code != 2 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if !strings.Contains(stderr.String(), "One question:") || !strings.Contains(stderr.String(), "Confirm these medium-confidence values:") {
		t.Fatalf("stderr = %s", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(repo, ".harness", "evals", "agents", "support", "agent.yaml")); !os.IsNotExist(err) {
		t.Fatal("non-TTY setup wrote medium-confidence files")
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

func TestProvisionalCommandsRegister(t *testing.T) {
	t.Setenv(hbase.EnvEnabledModules, "aievals")
	t.Setenv(hbase.EnvCLIHome, t.TempDir())
	reg := registry.New()
	if err := specloader.LoadSpecs(reg); err != nil {
		t.Fatal(err)
	}
	ModuleInit(reg.Module("aievals"))
	for _, verb := range []string{"setup", "run", "upload"} {
		cs := reg.GetSpec(verb, "eval_onboarding")
		if cs == nil {
			t.Fatalf("missing %s eval_onboarding", verb)
		}
		if cs.WorkflowID != "aievals:"+verb {
			t.Fatalf("%s workflow = %s", verb, cs.WorkflowID)
		}
		if !strings.Contains(cs.Long, "TODO(onboarding-schema)") {
			t.Fatalf("%s is missing the provisional marker", verb)
		}
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
	if !strings.Contains(agentYAML, "identifier: "+agent) || !strings.Contains(agentYAML, "method: POST") || !strings.Contains(agentYAML, "response_path: output.text") {
		t.Fatalf("agent yaml:\n%s", agentYAML)
	}
	if !strings.Contains(agentYAML, "endpoint: http://127.0.0.1:"+port+path) {
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
	if !strings.Contains(metric, "kind: exact_match") || !strings.Contains(metric, "identifier: "+agent) {
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
