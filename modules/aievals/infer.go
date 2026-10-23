// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type Confidence int

const (
	ConfidenceHigh Confidence = iota
	ConfidenceMedium
	ConfidenceLow
)

func (c Confidence) String() string {
	switch c {
	case ConfidenceHigh:
		return "high"
	case ConfidenceMedium:
		return "medium"
	default:
		return "low"
	}
}

// FieldEvidence is one inferred fact. Value is empty for a low-confidence gap.
type FieldEvidence struct {
	Field      string
	Value      string
	Confidence Confidence
	Source     string
}

// Inference is the scan result tests and setup share.
type Inference struct {
	Language string
	AgentID  string
	Evidence []FieldEvidence
	Files    map[string]string
}

func (inf Inference) ByField(name string) (FieldEvidence, bool) {
	for _, ev := range inf.Evidence {
		if ev.Field == name {
			return ev, true
		}
	}
	return FieldEvidence{}, false
}

func (inf Inference) Medium() []FieldEvidence {
	return inf.filter(ConfidenceMedium)
}

func (inf Inference) Low() []FieldEvidence {
	return inf.filter(ConfidenceLow)
}

func (inf Inference) filter(level Confidence) []FieldEvidence {
	var out []FieldEvidence
	for _, ev := range inf.Evidence {
		if ev.Confidence == level {
			out = append(out, ev)
		}
	}
	return out
}

// FormatMediumConfirmation is the single medium-confidence batch.
func FormatMediumConfirmation(inf Inference) string {
	items := inf.Medium()
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Confirm these medium-confidence values:\n")
	for _, ev := range items {
		fmt.Fprintf(&b, "- %s = %s (%s)\n", ev.Field, ev.Value, ev.Source)
	}
	return b.String()
}

// FormatLowQuestion is the single low-confidence question. It names variables
// and never asks for a secret value.
func FormatLowQuestion(inf Inference) string {
	items := inf.Low()
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("One question: these values had no reliable evidence. Reply with names only, never secret values.\n")
	for _, ev := range items {
		switch ev.Field {
		case "auth_env":
			header, _ := inf.ByField("auth_header")
			fmt.Fprintf(&b, "- auth_env: which environment variable name supplies the %s header?\n", header.Value)
		default:
			fmt.Fprintf(&b, "- %s: what is the value? (source gap %s)\n", ev.Field, ev.Source)
		}
	}
	return b.String()
}

var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "target": true,
	"build": true, "dist": true, ".harness": true,
}

// Infer scans repo. Typed Java, TypeScript, and typed Python win over untyped
// route literals. The candidate Files include high and medium values only.
func Infer(repo string) (Inference, error) {
	files, err := readRepoFiles(repo)
	if err != nil {
		return Inference{}, err
	}
	var inf Inference
	switch {
	case javaTyped(files):
		inf = inferJava(files)
	case typescriptTyped(files):
		inf = inferTypeScript(files)
	case pythonTyped(files):
		inf = inferPython(files)
	default:
		inf = inferUntyped(files)
	}
	sort.Slice(inf.Evidence, func(i, j int) bool {
		if inf.Evidence[i].Field == inf.Evidence[j].Field {
			return inf.Evidence[i].Source < inf.Evidence[j].Source
		}
		return inf.Evidence[i].Field < inf.Evidence[j].Field
	})
	inf.Files = materialize(inf, false, nil)
	return inf, nil
}

func readRepoFiles(repo string) (map[string]string, error) {
	out := map[string]string{}
	err := filepath.WalkDir(repo, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if path != repo && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		switch ext {
		case ".java", ".ts", ".tsx", ".py", ".js", ".properties", ".yml", ".yaml":
		default:
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > 1<<20 {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(repo, path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	return out, err
}

func javaTyped(files map[string]string) bool {
	for path, body := range files {
		if !strings.HasSuffix(path, ".java") {
			continue
		}
		if strings.Contains(body, "@RestController") || strings.Contains(body, "@PostMapping") || strings.Contains(body, "@GetMapping") {
			return true
		}
	}
	return false
}

func typescriptTyped(files map[string]string) bool {
	var hasInterface, hasTypedRoute bool
	for path, body := range files {
		if !strings.HasSuffix(path, ".ts") && !strings.HasSuffix(path, ".tsx") {
			continue
		}
		if strings.Contains(body, "interface ") {
			hasInterface = true
		}
		if regexp.MustCompile(`\)\s*:\s*[A-Z]`).MatchString(body) && strings.Contains(body, ".post(") {
			hasTypedRoute = true
		}
		if strings.Contains(body, "req: ") && (strings.Contains(body, ".post(") || strings.Contains(body, ".get(")) {
			hasTypedRoute = true
		}
	}
	return hasInterface && hasTypedRoute
}

func pythonTyped(files map[string]string) bool {
	var hasModel, hasRoute bool
	for path, body := range files {
		if !strings.HasSuffix(path, ".py") {
			continue
		}
		if strings.Contains(body, "(BaseModel)") {
			hasModel = true
		}
		if strings.Contains(body, "@app.post") || strings.Contains(body, "@app.get") {
			hasRoute = true
		}
	}
	return hasModel && hasRoute
}

type located struct {
	path string
	body string
}

func filesWithSuffix(files map[string]string, suffixes ...string) []located {
	var out []located
	for path, body := range files {
		for _, suffix := range suffixes {
			if strings.HasSuffix(path, suffix) {
				out = append(out, located{path, body})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out
}

func sourceAt(path, body, needle string) string {
	idx := strings.Index(body, needle)
	if idx < 0 {
		return path + ":1"
	}
	return fmt.Sprintf("%s:%d", path, strings.Count(body[:idx], "\n")+1)
}

func add(ev *[]FieldEvidence, field, value, source string, level Confidence) {
	*ev = append(*ev, FieldEvidence{Field: field, Value: value, Confidence: level, Source: source})
}

var (
	reRequestMapping = regexp.MustCompile(`@RequestMapping\(\s*"([^"]+)"\s*\)`)
	rePostMapping    = regexp.MustCompile(`@PostMapping\(\s*"([^"]+)"\s*\)`)
	reGetMapping     = regexp.MustCompile(`@GetMapping\(\s*"([^"]+)"\s*\)`)
	reRequestHeader  = regexp.MustCompile(`@RequestHeader\(\s*"([^"]+)"\s*\)`)
	reServerPort     = regexp.MustCompile(`(?m)^server\.port\s*=\s*(\d+)\s*$`)
	reGetenv         = regexp.MustCompile(`System\.getenv\(\s*"([A-Za-z_][A-Za-z0-9_]*)"\s*\)`)
	reJSONPath       = regexp.MustCompile(`jsonPath\(\s*"\$\.([A-Za-z0-9_.]+)"\s*\)`)
	reJavaPost       = regexp.MustCompile(`post\(\s*"([^"]+)"\s*\)`)
	reJavaContent    = regexp.MustCompile(`content\(\s*"((?:\\.|[^"\\])*)"\s*\)`)
	reJSONValue      = regexp.MustCompile(`\.value\(\s*"([^"]*)"\s*\)`)
	rePackage        = regexp.MustCompile(`(?m)^package\s+([A-Za-z0-9_.]+)\s*;`)
	rePrivateField   = regexp.MustCompile(`private\s+(\w+)\s+(\w+)\s*;`)
	reClass          = regexp.MustCompile(`class\s+(\w+)`)
)

func inferJava(files map[string]string) Inference {
	var ev []FieldEvidence
	agent := "app"
	var classPrefix, methodPath, method string
	var methodSource, pathSource string
	for _, f := range filesWithSuffix(files, ".java") {
		if m := rePackage.FindStringSubmatch(f.body); m != nil {
			parts := strings.Split(m[1], ".")
			agent = parts[len(parts)-1]
		}
		if strings.Contains(f.body, "@SpringBootApplication") {
			add(&ev, "entrypoint", f.path, sourceAt(f.path, f.body, "@SpringBootApplication"), ConfidenceHigh)
		}
		if m := reRequestMapping.FindStringSubmatch(f.body); m != nil {
			classPrefix = m[1]
			pathSource = sourceAt(f.path, f.body, m[0])
		}
		if m := rePostMapping.FindStringSubmatch(f.body); m != nil {
			methodPath = m[1]
			method = "POST"
			methodSource = sourceAt(f.path, f.body, m[0])
		} else if m := reGetMapping.FindStringSubmatch(f.body); m != nil {
			methodPath = m[1]
			method = "GET"
			methodSource = sourceAt(f.path, f.body, m[0])
		}
		if m := reRequestHeader.FindStringSubmatch(f.body); m != nil {
			add(&ev, "auth_header", m[1], sourceAt(f.path, f.body, m[0]), ConfidenceHigh)
		}
	}
	endpoint := joinURLPath(classPrefix, methodPath)
	corroborated := false
	for _, f := range filesWithSuffix(files, ".java") {
		if !strings.Contains(f.path, "Test") && !strings.Contains(f.path, "/test/") {
			continue
		}
		if m := reJavaPost.FindStringSubmatch(f.body); m != nil && m[1] == endpoint {
			corroborated = true
		}
		if m := reGetenv.FindStringSubmatch(f.body); m != nil {
			add(&ev, "auth_env", m[1], sourceAt(f.path, f.body, m[0]), ConfidenceHigh)
		}
		if m := reJSONPath.FindStringSubmatch(f.body); m != nil {
			add(&ev, "response_path", m[1], sourceAt(f.path, f.body, m[0]), ConfidenceHigh)
		}
		if m := reJavaContent.FindStringSubmatch(f.body); m != nil {
			if text := jsonTextField(unescapeJava(m[1])); text != "" {
				add(&ev, "test_input_text", text, sourceAt(f.path, f.body, m[0]), ConfidenceHigh)
			}
		}
		if m := reJSONValue.FindStringSubmatch(f.body); m != nil {
			add(&ev, "test_expected", m[1], sourceAt(f.path, f.body, m[0]), ConfidenceHigh)
		}
	}
	level := ConfidenceMedium
	if corroborated {
		level = ConfidenceHigh
	}
	if endpoint != "" {
		add(&ev, "endpoint_path", endpoint, pathSource, level)
	}
	if method != "" {
		add(&ev, "method", method, methodSource, level)
	}
	for _, f := range filesWithSuffix(files, ".properties") {
		if m := reServerPort.FindStringSubmatch(f.body); m != nil {
			add(&ev, "port", m[1], sourceAt(f.path, f.body, m[0]), ConfidenceHigh)
		}
	}
	if _, ok := findField(ev, "response_path"); !ok {
		if path := javaResponsePath(files); path.value != "" {
			add(&ev, "response_path", path.value, path.source, ConfidenceHigh)
		}
	}
	if _, ok := findField(ev, "test_input_text"); ok {
		add(&ev, "request_field", "text", "request-dto", ConfidenceHigh)
	}
	add(&ev, "agent_id", agent, "package", ConfidenceHigh)
	return Inference{Language: "java", AgentID: agent, Evidence: ev}
}

type sourcedValue struct {
	value  string
	source string
}

func javaResponsePath(files map[string]string) sourcedValue {
	classes := map[string][]string{}
	var source string
	for _, f := range filesWithSuffix(files, ".java") {
		classesInFile := map[string][]string{}
		current := ""
		for _, line := range strings.Split(f.body, "\n") {
			if m := reClass.FindStringSubmatch(line); m != nil {
				current = m[1]
			}
			if m := rePrivateField.FindStringSubmatch(line); m != nil && current != "" {
				classesInFile[current] = append(classesInFile[current], m[1]+" "+m[2])
				classes[current] = append(classes[current], m[1]+" "+m[2])
			}
		}
		for name, fields := range classesInFile {
			if strings.HasSuffix(name, "Response") {
				source = f.path + ":1"
				for _, field := range fields {
					typ, fname, _ := strings.Cut(field, " ")
					if typ == "String" {
						return sourcedValue{fname, source}
					}
					for _, inner := range classes[typ] {
						it, iname, _ := strings.Cut(inner, " ")
						if it == "String" {
							return sourcedValue{fname + "." + iname, source}
						}
					}
				}
			}
		}
	}
	return sourcedValue{}
}

func inferTypeScript(files map[string]string) Inference {
	return inferScript(files, "typescript", true)
}

func inferPython(files map[string]string) Inference {
	var ev []FieldEvidence
	agent := "app"
	for _, f := range filesWithSuffix(files, ".py") {
		if strings.Contains(f.path, "main.py") || strings.Contains(f.body, "uvicorn.run") {
			add(&ev, "entrypoint", f.path, sourceAt(f.path, f.body, "uvicorn.run"), ConfidenceHigh)
			if dir := filepath.Base(filepath.Dir(f.path)); dir != "." && dir != "/" {
				agent = dir
			}
		}
		if strings.Contains(f.body, "class ChatRequest") || regexp.MustCompile(`class\s+\w+Request\(BaseModel\)`).MatchString(f.body) {
			if strings.Contains(f.body, "text: str") {
				add(&ev, "request_field", "text", sourceAt(f.path, f.body, "text: str"), ConfidenceHigh)
			}
		}
		if m := regexp.MustCompile(`@app\.(post|get|put|patch|delete)\(\s*"([^"]+)"\s*\)`).FindStringSubmatch(f.body); m != nil {
			add(&ev, "method", strings.ToUpper(m[1]), sourceAt(f.path, f.body, m[0]), ConfidenceHigh)
			add(&ev, "endpoint_path", m[2], sourceAt(f.path, f.body, m[0]), ConfidenceHigh)
		}
		if m := regexp.MustCompile(`(?i)port\s*=\s*(\d+)`).FindStringSubmatch(f.body); m != nil {
			add(&ev, "port", m[1], sourceAt(f.path, f.body, m[0]), ConfidenceHigh)
		}
		if strings.Contains(f.body, "class Output") && strings.Contains(f.body, "text: str") && strings.Contains(f.body, "output: Output") {
			add(&ev, "response_path", "output.text", sourceAt(f.path, f.body, "output: Output"), ConfidenceHigh)
		}
		if strings.Contains(f.path, "test") {
			if m := regexp.MustCompile(`os\.environ\[\s*"([A-Za-z_][A-Za-z0-9_]*)"\s*\]`).FindStringSubmatch(f.body); m != nil {
				add(&ev, "auth_env", m[1], sourceAt(f.path, f.body, m[0]), ConfidenceHigh)
			}
			if strings.Contains(f.body, `"Authorization"`) || strings.Contains(f.body, "Authorization") {
				add(&ev, "auth_header", "Authorization", sourceAt(f.path, f.body, "Authorization"), ConfidenceHigh)
			}
			if m := regexp.MustCompile(`json\s*=\s*\{\s*"text"\s*:\s*"([^"]+)"\s*\}`).FindStringSubmatch(f.body); m != nil {
				add(&ev, "test_input_text", m[1], sourceAt(f.path, f.body, m[0]), ConfidenceHigh)
			}
			if m := regexp.MustCompile(`==\s*"([^"]+)"`).FindStringSubmatch(f.body); m != nil {
				add(&ev, "test_expected", m[1], sourceAt(f.path, f.body, m[0]), ConfidenceHigh)
			}
		}
	}
	if _, ok := findField(ev, "agent_id"); !ok {
		add(&ev, "agent_id", agent, "module", ConfidenceHigh)
	}
	return Inference{Language: "python", AgentID: agentID(ev, agent), Evidence: ev}
}

func inferScript(files map[string]string, language string, typed bool) Inference {
	level := ConfidenceHigh
	if !typed {
		level = ConfidenceMedium
	}
	var ev []FieldEvidence
	agent := "app"
	for _, f := range filesWithSuffix(files, ".ts", ".tsx", ".js") {
		if strings.Contains(f.body, "interface ChatRequest") || strings.Contains(f.body, "text: string") && strings.Contains(f.body, "ChatRequest") {
			add(&ev, "request_field", "text", sourceAt(f.path, f.body, "text: string"), level)
		}
		if strings.Contains(f.body, "output:") && strings.Contains(f.body, "text: string") && strings.Contains(f.body, "ChatResponse") {
			add(&ev, "response_path", "output.text", sourceAt(f.path, f.body, "output:"), level)
		}
		if m := regexp.MustCompile(`\.post\(\s*"([^"]+)"`).FindStringSubmatch(f.body); m != nil && !strings.Contains(f.path, "test") {
			add(&ev, "method", "POST", sourceAt(f.path, f.body, m[0]), level)
			add(&ev, "endpoint_path", m[1], sourceAt(f.path, f.body, m[0]), level)
			add(&ev, "entrypoint", f.path, sourceAt(f.path, f.body, m[0]), level)
			if dir := serviceDir(f.path); dir != "" {
				agent = dir
			}
		}
		if m := regexp.MustCompile(`\.listen\(\s*(\d+)\s*\)`).FindStringSubmatch(f.body); m != nil {
			add(&ev, "port", m[1], sourceAt(f.path, f.body, m[0]), level)
		}
		if strings.Contains(f.path, "test") {
			if m := regexp.MustCompile(`process\.env\.([A-Za-z_][A-Za-z0-9_]*)`).FindStringSubmatch(f.body); m != nil {
				add(&ev, "auth_env", m[1], sourceAt(f.path, f.body, m[0]), level)
			}
			if strings.Contains(f.body, `"Authorization"`) || strings.Contains(f.body, ".set(\"Authorization\"") {
				add(&ev, "auth_header", "Authorization", sourceAt(f.path, f.body, "Authorization"), level)
			}
			if m := regexp.MustCompile(`text:\s*"([^"]+)"`).FindStringSubmatch(f.body); m != nil {
				if strings.Contains(f.body, "send(") {
					add(&ev, "test_input_text", m[1], sourceAt(f.path, f.body, "send("), level)
				}
			}
			if m := regexp.MustCompile(`expect\(\s*\{\s*output:\s*\{\s*text:\s*"([^"]+)"`).FindStringSubmatch(f.body); m != nil {
				add(&ev, "test_expected", m[1], sourceAt(f.path, f.body, m[0]), level)
			}
		}
	}
	add(&ev, "agent_id", agent, "package", level)
	return Inference{Language: language, AgentID: agent, Evidence: ev}
}

func inferUntyped(files map[string]string) Inference {
	var ev []FieldEvidence
	agent := "app"
	for _, f := range filesWithSuffix(files, ".js") {
		if m := regexp.MustCompile(`app\.post\(\s*"([^"]+)"`).FindStringSubmatch(f.body); m != nil {
			add(&ev, "entrypoint", f.path, sourceAt(f.path, f.body, m[0]), ConfidenceMedium)
			add(&ev, "method", "POST", sourceAt(f.path, f.body, m[0]), ConfidenceMedium)
			add(&ev, "endpoint_path", m[1], sourceAt(f.path, f.body, m[0]), ConfidenceMedium)
			if dir := serviceDir(f.path); dir != "" {
				agent = dir
			}
		}
		if m := regexp.MustCompile(`\.listen\(\s*(\d+)\s*\)`).FindStringSubmatch(f.body); m != nil {
			add(&ev, "port", m[1], sourceAt(f.path, f.body, m[0]), ConfidenceMedium)
		}
		if strings.Contains(f.body, "output") && strings.Contains(f.body, "text") && strings.Contains(f.body, "assert") {
			add(&ev, "response_path", "output.text", sourceAt(f.path, f.body, "output"), ConfidenceMedium)
		}
		if strings.Contains(f.body, "Authorization") {
			add(&ev, "auth_header", "Authorization", sourceAt(f.path, f.body, "Authorization"), ConfidenceMedium)
		}
		if regexp.MustCompile(`process\.env\.[A-Za-z_]`).MatchString(f.body) || regexp.MustCompile(`os\.environ`).MatchString(f.body) {
			if m := regexp.MustCompile(`process\.env\.([A-Za-z_][A-Za-z0-9_]*)`).FindStringSubmatch(f.body); m != nil {
				add(&ev, "auth_env", m[1], sourceAt(f.path, f.body, m[0]), ConfidenceMedium)
			}
		} else if strings.Contains(f.body, "Authorization") {
			add(&ev, "auth_env", "", sourceAt(f.path, f.body, "Authorization"), ConfidenceLow)
		}
		if m := regexp.MustCompile(`text:\s*"([^"]+)"`).FindStringSubmatch(f.body); m != nil && strings.Contains(f.body, "JSON.stringify") {
			add(&ev, "test_input_text", m[1], sourceAt(f.path, f.body, m[0]), ConfidenceMedium)
			add(&ev, "request_field", "text", sourceAt(f.path, f.body, m[0]), ConfidenceMedium)
		}
		if m := regexp.MustCompile(`assert\.equal\([^,]+,\s*"([^"]+)"\s*\)`).FindStringSubmatch(f.body); m != nil {
			add(&ev, "test_expected", m[1], sourceAt(f.path, f.body, m[0]), ConfidenceMedium)
		}
	}
	add(&ev, "agent_id", agent, "directory", ConfidenceMedium)
	return Inference{Language: "untyped", AgentID: agent, Evidence: ev}
}

func serviceDir(path string) string {
	base := filepath.Base(filepath.Dir(path))
	switch base {
	case ".", "src", "test", "tests", "main", "app":
		return ""
	default:
		return base
	}
}

func findField(ev []FieldEvidence, name string) (FieldEvidence, bool) {
	for _, item := range ev {
		if item.Field == name {
			return item, true
		}
	}
	return FieldEvidence{}, false
}

func agentID(ev []FieldEvidence, fallback string) string {
	if item, ok := findField(ev, "agent_id"); ok && item.Value != "" {
		return item.Value
	}
	return fallback
}

func joinURLPath(prefix, methodPath string) string {
	if prefix == "" {
		return methodPath
	}
	if methodPath == "" {
		return prefix
	}
	return strings.TrimRight(prefix, "/") + "/" + strings.TrimLeft(methodPath, "/")
}

func unescapeJava(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, `\"`, `"`), `\\`, `\`)
}

func jsonTextField(raw string) string {
	m := regexp.MustCompile(`"text"\s*:\s*"([^"]*)"`).FindStringSubmatch(raw)
	if m == nil {
		return ""
	}
	return m[1]
}
