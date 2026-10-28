// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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

type FieldEvidence struct {
	Field      string
	Value      string
	Confidence Confidence
	Source     string
}

type Inference struct {
	Language             string
	AgentID              string
	Evidence             []FieldEvidence
	Files                map[string]string
	HasSignal            bool
	Notes                []string
	LocationQuestion     string
	CandidatePaths       []string
	RouteCandidates      []RouteCandidate
	EvidenceFilesScanned int
}

type RouteCandidate struct {
	Method        string
	Path          string
	QueryParams   []string
	PathParams    []string
	QueryMappings []string
	PathMappings  []string
	Header        string
	AuthRequired  bool
	Agent         string
	ServiceRoot   string
	Adapter       string
	Source        string
	Score         int
	Evidence      []FieldEvidence

	request  *dtoShape
	response *dtoShape
}

func (c RouteCandidate) Identity() string {
	return fmt.Sprintf("%s [%s %s]", c.Route(), c.ServiceRoot, c.Source)
}

func (c RouteCandidate) Route() string {
	return canonicalRouteIdentity(c.Method, c.Path)
}

type AmbiguousRouteError struct {
	Candidates []RouteCandidate
}

func (e *AmbiguousRouteError) Error() string {
	var b strings.Builder
	b.WriteString("multiple plausible routes found; select one with --route '<METHOD path>' before staging (append ' [<service root>]' when the same route exists in more than one service):\n")
	for _, candidate := range e.Candidates {
		fmt.Fprintf(&b, "- %s (score %d)\n", candidate.Identity(), candidate.Score)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func (inf Inference) RouteAmbiguity() error {
	if len(inf.RouteCandidates) < 2 {
		return nil
	}
	return &AmbiguousRouteError{Candidates: append([]RouteCandidate(nil), inf.RouteCandidates...)}
}

func (inf Inference) ByField(name string) (FieldEvidence, bool) {
	for _, ev := range inf.Evidence {
		if ev.Field == name {
			return ev, true
		}
	}
	return FieldEvidence{}, false
}

func (inf Inference) Medium() []FieldEvidence { return inf.filter(ConfidenceMedium) }
func (inf Inference) Low() []FieldEvidence    { return inf.filter(ConfidenceLow) }

func (inf Inference) filter(level Confidence) []FieldEvidence {
	var out []FieldEvidence
	for _, ev := range inf.Evidence {
		if ev.Confidence == level {
			out = append(out, ev)
		}
	}
	return out
}

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

func FormatLowQuestion(inf Inference) string {
	if inf.LocationQuestion != "" {
		return inf.LocationQuestion
	}
	var items []FieldEvidence
	for _, ev := range inf.Low() {
		if ev.Field == "auth_env" || ev.Field == "auth_header" || ev.Field == "expected_gap" || isFeatureFlag(ev.Field) || isFeatureFlag(ev.Value) {
			continue
		}
		items = append(items, ev)
	}
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("One question: these values had no reliable evidence. Reply with names only, never secret values.\n")
	for _, ev := range items {
		fmt.Fprintf(&b, "- %s: what is the value? (source gap %s)\n", ev.Field, ev.Source)
	}
	return b.String()
}

var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "target": true,
	"build": true, "dist": true, ".harness": true, ".venv": true,
	"venv": true, "site-packages": true, "__pycache__": true, ".next": true,
	"coverage": true, ".gradle": true, "generated": true,
}

// Infer has one discovery path: service-scoped supported framework adapters.
func Infer(repo string) (Inference, error) { return inferSupportedAdapters(repo, "") }

func InferWithRoute(repo, route string) (Inference, error) {
	return inferSupportedAdapters(repo, route)
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
		switch strings.ToLower(filepath.Ext(path)) {
		case ".java", ".ts", ".tsx", ".mts", ".cts", ".py", ".js", ".mjs", ".cjs":
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

type located struct{ path, body string }

func allTextFiles(files map[string]string) []located {
	var out []located
	for path, body := range files {
		out = append(out, located{path, body})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out
}

func add(ev *[]FieldEvidence, field, value, source string, level Confidence) {
	*ev = append(*ev, FieldEvidence{Field: field, Value: value, Confidence: level, Source: source})
}

func findField(ev []FieldEvidence, name string) (FieldEvidence, bool) {
	for _, item := range ev {
		if item.Field == name {
			return item, true
		}
	}
	return FieldEvidence{}, false
}

func normalizeRouteSelection(selection string) string {
	parts := strings.Fields(selection)
	if len(parts) < 2 {
		return selection
	}
	return canonicalRouteIdentity(parts[0], strings.Join(parts[1:], " "))
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

func finalizeInference(inf Inference, repo string) Inference {
	if len(inf.RouteCandidates) > 1 && !evidenceSignal(inf) {
		return inf
	}
	if evidenceSignal(inf) {
		inf.HasSignal = true
	}
	if !inf.HasSignal {
		return inf
	}
	if inf.AgentID == "" {
		inf.AgentID = agentFromDir(repo)
	}
	if _, ok := findField(inf.Evidence, "agent_id"); !ok {
		add(&inf.Evidence, "agent_id", inf.AgentID, "directory", ConfidenceHigh)
	}
	if _, ok := findField(inf.Evidence, "method"); !ok {
		add(&inf.Evidence, "method", "GET", "default", ConfidenceHigh)
	}
	if _, ok := findField(inf.Evidence, "endpoint_path"); !ok {
		add(&inf.Evidence, "endpoint_path", "/", "default", ConfidenceHigh)
	}
	if _, ok := findField(inf.Evidence, "port"); !ok {
		add(&inf.Evidence, "port", "8080", "default", ConfidenceHigh)
		inf.Notes = append(inf.Notes, "Port was not evidenced; using 8080.")
	}
	if _, ok := findField(inf.Evidence, "response_path"); !ok {
		add(&inf.Evidence, "response_path", "output.text", "default", ConfidenceHigh)
	}
	if _, ok := findField(inf.Evidence, "request_field"); !ok {
		add(&inf.Evidence, "request_field", "text", "default", ConfidenceHigh)
	}
	if _, ok := findField(inf.Evidence, "test_expected"); !ok {
		resp := "output.text"
		if ev, ok := findField(inf.Evidence, "response_path"); ok && ev.Value != "" {
			resp = ev.Value
		}
		gap := "expected value was not evidenced for " + resp
		add(&inf.Evidence, "expected_gap", gap, "gap", ConfidenceHigh)
		inf.Notes = append(inf.Notes, gap)
	}
	return inf
}

func evidenceSignal(inf Inference) bool {
	for _, name := range []string{"endpoint_path", "method"} {
		if ev, ok := findField(inf.Evidence, name); ok && ev.Value != "" {
			return true
		}
	}
	return false
}

func agentFromDir(repo string) string {
	base := filepath.Base(filepath.Clean(repo))
	switch base {
	case "", ".", string(filepath.Separator), "src", "test", "tests", "main", "app":
		return "app"
	default:
		return slugIdentifier(base)
	}
}

func slugIdentifier(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-' {
			b.WriteRune(char)
			lastDash = char == '-'
		} else if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	if out := strings.Trim(b.String(), "-"); out != "" {
		return out
	}
	return "app"
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, item := range in {
		if item != "" && !seen[item] {
			seen[item] = true
			out = append(out, item)
		}
	}
	return out
}

func routeCandidateIdentities(candidates []RouteCandidate) []string {
	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, candidate.Identity())
	}
	return out
}

func canonicalRouteIdentity(method, routePath string) string {
	return strings.ToUpper(strings.TrimSpace(method)) + " " + canonicalRoutePath(routePath)
}

func canonicalRoutePath(routePath string) string {
	routePath = strings.TrimSpace(routePath)
	if strings.Contains(routePath, "://") {
		_, routePath = splitEndpointURL(routePath)
	}
	if !strings.HasPrefix(routePath, "/") {
		routePath = "/" + routePath
	}
	if routePath != "/" {
		routePath = strings.TrimRight(routePath, "/")
	}
	return routePath
}

func packageAgent(pkg string) string {
	parts := strings.Split(pkg, ".")
	generic := map[string]bool{"resource": true, "resources": true, "api": true, "controller": true, "controllers": true, "service": true, "services": true, "impl": true, "internal": true, "rest": true, "endpoint": true, "endpoints": true, "handler": true, "handlers": true, "app": true, "main": true, "java": true, "web": true, "com": true, "org": true, "io": true, "net": true, "example": true}
	for i := len(parts) - 1; i >= 0; i-- {
		if !generic[parts[i]] {
			return parts[i]
		}
	}
	return parts[len(parts)-1]
}

func canonicalHeader(name string) string {
	switch strings.ToLower(name) {
	case "authorization":
		return "Authorization"
	case "x-api-key":
		return "x-api-key"
	default:
		return name
	}
}

func isFeatureFlag(name string) bool {
	u := strings.ToUpper(strings.TrimSpace(name))
	return strings.HasPrefix(u, "TR_") || strings.Contains(u, "FEATURE") || strings.Contains(u, "FLAG") || strings.HasSuffix(u, "_ENABLED") || strings.HasSuffix(u, "_MODE")
}

func splitEndpointURL(raw string) (port, path string) {
	raw = strings.TrimRight(strings.TrimSpace(raw), ".,);\"'")
	raw = strings.TrimPrefix(strings.TrimPrefix(raw, "http://"), "https://")
	path = "/"
	host := raw
	if i := strings.Index(raw, "/"); i >= 0 {
		host, path = raw[:i], raw[i:]
		if q := strings.IndexAny(path, "?#"); q >= 0 {
			path = path[:q]
		}
	}
	if i := strings.LastIndex(host, ":"); i >= 0 {
		if _, err := strconv.Atoi(host[i+1:]); err == nil {
			port = host[i+1:]
		}
	}
	return port, path
}
