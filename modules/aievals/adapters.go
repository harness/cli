// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	ts "github.com/odvcencio/gotreesitter"
)

// Route inference is adapter-based and AST-backed. A candidate exists only
// when a supported framework's route syntax is proven by the syntax tree:
// Spring/JAX-RS/Micronaut annotations resolved through imports, FastAPI
// decorators on imported FastAPI/APIRouter instances, and Express methods on
// imported express() apps or mounted routers. Anything else is reported as a
// note and never staged.

type dtoShapeField struct {
	name     string
	isString bool
	nested   *dtoShape
}

type dtoShape struct {
	fields []dtoShapeField
	source string
}

func (s *dtoShape) names() []string {
	if s == nil {
		return nil
	}
	out := make([]string, 0, len(s.fields))
	for _, field := range s.fields {
		out = append(out, field.name)
	}
	return out
}

// primary is the first string field, else the first field.
func (s *dtoShape) primary() string {
	if s == nil || len(s.fields) == 0 {
		return ""
	}
	for _, field := range s.fields {
		if field.isString {
			return field.name
		}
	}
	return s.fields[0].name
}

// responsePath is the first string field, or the first string field of a
// nested DTO, in declaration order.
func (s *dtoShape) responsePath() string {
	if s == nil {
		return ""
	}
	for _, field := range s.fields {
		if field.isString {
			return field.name
		}
		if field.nested != nil {
			for _, nested := range field.nested.fields {
				if nested.isString {
					return field.name + "." + nested.name
				}
			}
		}
	}
	return ""
}

type portEvidence struct {
	value  string
	source string
}

type keyValue struct {
	key    string
	value  string
	static bool
}

type testHeader struct {
	name   string
	env    string
	source string
}

type testAssertion struct {
	path   string
	value  string
	source string
}

// testCallFact is one request expression in a test file and only the
// headers, body, and assertions syntactically attached to it.
type testCallFact struct {
	route      string
	source     string
	headers    []testHeader
	body       []keyValue
	bodyStatic bool
	assertions []testAssertion
}

type adapterScan struct {
	candidates  []RouteCandidate
	notes       []string
	ports       map[string][]portEvidence
	calls       map[string][]testCallFact
	testScanned int
}

func inferSupportedAdapters(repo, selection string) (Inference, error) {
	files, err := readRepoFiles(repo)
	if err != nil {
		return Inference{}, err
	}
	scan := scanAdapters(files)
	candidates := sortCandidates(uniqueAdapterCandidates(scan.candidates))
	for i := range candidates {
		candidates[i].Evidence = routeCallEvidence(candidates[i], scan.calls[candidates[i].ServiceRoot])
	}
	inf := Inference{
		Language:             "adapter",
		RouteCandidates:      candidates,
		EvidenceFilesScanned: scan.testScanned,
		Notes:                append([]string(nil), scan.notes...),
	}
	if len(candidates) == 0 {
		return inf, nil
	}
	if len(candidates) > 1 && strings.TrimSpace(selection) == "" {
		inf.HasSignal = true
		inf.CandidatePaths = routeCandidateIdentities(candidates)
		return inf, nil
	}
	candidate, ok := selectAdapterRoute(candidates, selection)
	if !ok {
		inf.HasSignal = true
		inf.CandidatePaths = routeCandidateIdentities(candidates)
		inf.Notes = append(inf.Notes, fmt.Sprintf("Route %q did not match exactly one detected candidate.", sanitizeSourcePath(selection)))
		return inf, nil
	}
	inf.Language = candidate.Adapter
	inf.AgentID = candidate.Agent
	applyAdapterEvidence(&inf, candidate, scan.calls[candidate.ServiceRoot], scan.ports[portKey(candidate.ServiceRoot, candidate.Adapter)])
	inf = finalizeInference(inf, repo)
	sort.SliceStable(inf.Evidence, func(i, j int) bool { return inf.Evidence[i].Field < inf.Evidence[j].Field })
	inf.Files = materialize(inf, false, nil)
	return inf, nil
}

func portKey(root, adapter string) string {
	if adapter == "javascript" {
		adapter = "typescript"
	}
	return root + "\x00" + adapter
}

// scanAdapters parses each supported source once, extracts plain facts, and
// releases the tree before moving on.
func scanAdapters(files map[string]string) adapterScan {
	scan := adapterScan{ports: map[string][]portEvidence{}, calls: map[string][]testCallFact{}}
	parsers := parserSet{}
	javaByRoot := map[string][]*javaFacts{}
	pyByRoot := map[string][]*pyFacts{}
	tsByRoot := map[string][]*tsFacts{}
	var roots []string
	seenRoot := map[string]bool{}
	for _, file := range allTextFiles(files) {
		kind := sourceKindFor(file.path)
		if kind == sourceNone {
			continue
		}
		test := isAdapterTestFile(file.path)
		if !test && isEvidenceOnlyFile(file.path) {
			continue
		}
		root := adapterServiceRoot(file.path)
		if !seenRoot[root] {
			seenRoot[root] = true
			roots = append(roots, root)
		}
		parsed, err := parsers.parse(file.path, file.body)
		if test {
			scan.testScanned++
		}
		if err != nil {
			if test {
				scan.notes = append(scan.notes, fmt.Sprintf("Ignored test evidence in %s: %v.", sanitizeSourcePath(file.path), err))
			} else {
				scan.notes = append(scan.notes, fmt.Sprintf("Skipped %s: %v; no routes were read from it.", sanitizeSourcePath(file.path), err))
			}
			continue
		}
		switch {
		case test && kind == sourceJava:
			scan.calls[root] = append(scan.calls[root], extractJavaTestCalls(parsed)...)
		case test && kind == sourcePython:
			scan.calls[root] = append(scan.calls[root], extractPythonTestCalls(parsed)...)
		case test:
			scan.calls[root] = append(scan.calls[root], extractExpressTestCalls(parsed)...)
		case kind == sourceJava:
			facts := extractJavaFacts(parsed)
			javaByRoot[root] = append(javaByRoot[root], facts)
			scan.notes = append(scan.notes, facts.notes...)
		case kind == sourcePython:
			facts := extractPythonFacts(parsed)
			pyByRoot[root] = append(pyByRoot[root], facts)
			scan.notes = append(scan.notes, facts.notes...)
		default:
			facts := extractExpressFacts(parsed)
			tsByRoot[root] = append(tsByRoot[root], facts)
			scan.notes = append(scan.notes, facts.notes...)
		}
		parsed.release()
	}
	for _, root := range roots {
		if facts := javaByRoot[root]; len(facts) > 0 {
			scan.candidates = append(scan.candidates, javaServiceCandidates(root, facts)...)
		}
		if facts := pyByRoot[root]; len(facts) > 0 {
			candidates, ports := pythonServiceCandidates(root, facts)
			scan.candidates = append(scan.candidates, candidates...)
			scan.ports[portKey(root, "python")] = ports
		}
		if facts := tsByRoot[root]; len(facts) > 0 {
			candidates, ports := expressServiceCandidates(root, facts)
			scan.candidates = append(scan.candidates, candidates...)
			scan.ports[portKey(root, "typescript")] = ports
		}
	}
	return scan
}

func newAdapterRoute(adapter, root, method, rawPath, source string) RouteCandidate {
	return RouteCandidate{
		Method:      strings.ToUpper(method),
		Path:        canonicalRoutePath(rawPath),
		Agent:       adapterAgent(root),
		ServiceRoot: root,
		Adapter:     adapter,
		Source:      source,
	}
}

func isRouteVerb(method string) bool {
	switch method {
	case "GET", "POST", "PUT", "DELETE", "PATCH":
		return true
	default:
		return false
	}
}

func sameNode(a, b *ts.Node) bool {
	return a != nil && b != nil && a.StartByte() == b.StartByte() && a.EndByte() == b.EndByte() && a.Symbol() == b.Symbol()
}

// chainedCall returns the call whose receiver is n, as in n.next(...).
func chainedCall(f *syntaxFile, n *ts.Node, callType, objectField string) *ts.Node {
	parent := n.Parent()
	if f.typ(parent) == callType && sameNode(f.field(parent, objectField), n) {
		return parent
	}
	return nil
}

// enclosingCallOfArgument returns the call that takes n as a direct argument.
func enclosingCallOfArgument(f *syntaxFile, n *ts.Node, callType string) *ts.Node {
	list := n.Parent()
	if list == nil {
		return nil
	}
	switch f.typ(list) {
	case "argument_list", "arguments":
	default:
		return nil
	}
	call := list.Parent()
	if f.typ(call) != callType {
		return nil
	}
	return call
}

func validResponsePath(path string) bool {
	if path == "" {
		return false
	}
	for _, segment := range strings.Split(path, ".") {
		if segment == "" || strings.ContainsAny(segment, "[]*? ") {
			return false
		}
	}
	return true
}

// jsonObjectStrings decodes a JSON object literal, keeping key order.
func jsonObjectStrings(raw string) ([]keyValue, bool) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil, false
	}
	var out []keyValue
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, false
		}
		key, _ := token.(string)
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, false
		}
		var text string
		static := json.Unmarshal(value, &text) == nil
		out = append(out, keyValue{key: key, value: text, static: static})
	}
	return out, true
}

func uniqueAdapterCandidates(routes []RouteCandidate) []RouteCandidate {
	seen := map[string]bool{}
	var out []RouteCandidate
	for _, route := range routes {
		if route.Method == "" || route.Path == "" || isOperationalPath(route.Path) {
			continue
		}
		key := route.ServiceRoot + "\x00" + route.internalIdentity()
		if seen[key] {
			continue
		}
		seen[key] = true
		route.Score = routeScore(route)
		out = append(out, route)
	}
	return out
}

func routeScore(route RouteCandidate) int {
	score := 3
	if route.request != nil {
		score++
	}
	if route.response != nil {
		score++
	}
	if route.Header != "" || route.AuthRequired || len(route.QueryParams) > 0 || len(route.PathParams) > 0 {
		score++
	}
	return score
}

func sortCandidates(candidates []RouteCandidate) []RouteCandidate {
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Score != candidates[j].Score {
			return candidates[i].Score > candidates[j].Score
		}
		if candidates[i].ServiceRoot != candidates[j].ServiceRoot {
			return candidates[i].ServiceRoot < candidates[j].ServiceRoot
		}
		return candidates[i].internalIdentity() < candidates[j].internalIdentity()
	})
	return candidates
}

// selectAdapterRoute accepts "METHOD path", "METHOD path [service]", or the
// full "METHOD path [service source]" identity, and requires one match.
func selectAdapterRoute(candidates []RouteCandidate, selection string) (RouteCandidate, bool) {
	selection = strings.TrimSpace(selection)
	if len(candidates) == 1 && selection == "" {
		return candidates[0], true
	}
	route, service, source := selection, "", ""
	if open := strings.LastIndex(selection, " ["); open >= 0 && strings.HasSuffix(selection, "]") {
		qualifier := strings.Fields(selection[open+2 : len(selection)-1])
		route = selection[:open]
		if len(qualifier) > 0 {
			service = qualifier[0]
		}
		if len(qualifier) > 1 {
			source = qualifier[1]
		}
	}
	route = normalizeRouteSelection(route)
	var matched []RouteCandidate
	for _, candidate := range candidates {
		serviceMatches := service == "" || candidate.ServiceRoot == service || sanitizeSourcePath(candidate.ServiceRoot) == service
		sourceMatches := source == "" || candidate.Source == source || sanitizeSourcePath(candidate.Source) == source
		routeMatches := candidate.Route() == route || sanitizeSourcePath(candidate.Route()) == route
		if !routeMatches || !serviceMatches || !sourceMatches {
			continue
		}
		matched = append(matched, candidate)
	}
	if len(matched) == 1 {
		return matched[0], true
	}
	return RouteCandidate{}, false
}

type evidenceSet struct {
	items []FieldEvidence
}

// set records the first value seen for a field; callers add evidence in
// precedence order.
func (e *evidenceSet) set(field, value, source string) {
	if value == "" {
		return
	}
	if _, ok := findField(e.items, field); ok {
		return
	}
	add(&e.items, field, value, source, ConfidenceHigh)
}

func routeCallsFor(route RouteCandidate, calls []testCallFact) []testCallFact {
	var out []testCallFact
	for _, call := range calls {
		if call.route == route.Route() {
			out = append(out, call)
		}
	}
	return out
}

func chooseAssertion(call testCallFact, route RouteCandidate) (testAssertion, bool) {
	if path := route.response.responsePath(); path != "" {
		for _, assertion := range call.assertions {
			if assertion.path == path {
				return assertion, true
			}
		}
	}
	if len(call.assertions) == 1 {
		return call.assertions[0], true
	}
	return testAssertion{}, false
}

func chooseInput(call testCallFact, route RouteCandidate) (keyValue, bool) {
	if !call.bodyStatic {
		return keyValue{}, false
	}
	if primary := route.request.primary(); primary != "" {
		for _, item := range call.body {
			if item.key == primary && item.static {
				return item, true
			}
		}
	}
	var statics []keyValue
	for _, item := range call.body {
		if item.static {
			statics = append(statics, item)
		}
	}
	if len(statics) != 1 {
		return keyValue{}, false
	}
	if fields := route.request.names(); len(fields) > 0 && !stringIn(fields, statics[0].key) {
		return keyValue{}, false
	}
	return statics[0], true
}

// routeCallEvidence picks the test case for a route: the first exact
// method+path call with an attributable assertion, else one with input.
func routeCallEvidence(route RouteCandidate, calls []testCallFact) []FieldEvidence {
	matched := routeCallsFor(route, calls)
	var chosen *testCallFact
	for i := range matched {
		if _, ok := chooseAssertion(matched[i], route); ok {
			chosen = &matched[i]
			break
		}
	}
	if chosen == nil {
		for i := range matched {
			if _, ok := chooseInput(matched[i], route); ok {
				chosen = &matched[i]
				break
			}
		}
	}
	if chosen == nil {
		return nil
	}
	var ev evidenceSet
	if input, ok := chooseInput(*chosen, route); ok {
		ev.set("request_field", input.key, chosen.source)
		ev.set("test_input_text", input.value, chosen.source)
	}
	if assertion, ok := chooseAssertion(*chosen, route); ok {
		ev.set("response_path", assertion.path, assertion.source)
		ev.set("test_expected", assertion.value, assertion.source)
	}
	return ev.items
}

func stringIn(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func isAuthHeaderName(name string) bool {
	switch canonicalHeader(name) {
	case "Authorization", "x-api-key":
		return true
	default:
		return false
	}
}

// routeAuth combines the route's declared header with secret env names that
// tests attach to the same header on calls to this exact route.
func routeAuth(route RouteCandidate, calls []testCallFact) (header, headerSource, env, envSource, note string) {
	header, headerSource = route.Header, route.Source
	literalHeader, literalSource := "", ""
	pairs := map[string]testHeader{}
	for _, call := range routeCallsFor(route, calls) {
		for _, item := range call.headers {
			name := canonicalHeader(item.name)
			if name == "" || (header != "" && !strings.EqualFold(name, header)) {
				continue
			}
			if item.env == "" || isFeatureFlag(item.env) {
				if literalHeader == "" && isAuthHeaderName(name) {
					literalHeader, literalSource = name, item.source
				}
				continue
			}
			item.name = name
			pairs[name+"\x00"+item.env] = item
		}
	}
	switch len(pairs) {
	case 0:
	case 1:
		for _, item := range pairs {
			if header == "" {
				header, headerSource = item.name, item.source
			}
			env, envSource = item.env, item.source
		}
	default:
		note = fmt.Sprintf("Tests for %s set conflicting secret environment names; authentication was omitted.", route.Route())
	}
	if header == "" {
		header, headerSource = literalHeader, literalSource
	}
	return header, headerSource, env, envSource, note
}

func choosePort(route RouteCandidate, ports []portEvidence) (portEvidence, string) {
	values := map[string]portEvidence{}
	for _, port := range ports {
		if previous, ok := values[port.value]; !ok || port.source < previous.source {
			values[port.value] = port
		}
	}
	if len(values) == 1 {
		for _, port := range values {
			return port, ""
		}
	}
	if len(values) > 1 {
		var found []string
		for value := range values {
			found = append(found, value)
		}
		sort.Strings(found)
		return portEvidence{}, fmt.Sprintf("Service %s declares conflicting ports (%s); none was selected.", sanitizeSourcePath(route.ServiceRoot), strings.Join(found, ", "))
	}
	return portEvidence{}, ""
}

func applyAdapterEvidence(inf *Inference, route RouteCandidate, calls []testCallFact, ports []portEvidence) {
	var ev evidenceSet
	ev.set("endpoint_path", route.Path, route.Source)
	ev.set("method", route.Method, route.Source)
	ev.set("agent_id", route.Agent, route.Source)
	if len(route.QueryParams) > 0 {
		ev.set("required_query_params", strings.Join(route.QueryParams, ","), route.Source)
		ev.set("query_param_mappings", strings.Join(route.QueryMappings, ","), route.Source)
		inf.Notes = append(inf.Notes, "Required query parameters were detected; configure their values before running the staged eval.")
	}
	if len(route.PathParams) > 0 {
		ev.set("required_path_params", strings.Join(route.PathParams, ","), route.Source)
		ev.set("path_param_mappings", strings.Join(route.PathMappings, ","), route.Source)
		inf.Notes = append(inf.Notes, "Required path parameters were detected; configure their values before running the staged eval.")
	}
	header, headerSource, env, envSource, note := routeAuth(route, calls)
	if note != "" {
		inf.Notes = append(inf.Notes, note)
	}
	ev.set("auth_header", header, headerSource)
	if header != "" {
		ev.set("auth_env", env, envSource)
	} else if route.AuthRequired {
		inf.Notes = append(inf.Notes, "@HarnessAuth was detected without an explicit header and secret reference; authentication was omitted from the staged target.")
	}
	if fields := route.request.names(); len(fields) > 0 {
		ev.set("request_fields", strings.Join(fields, ","), route.request.source)
	}
	for _, item := range route.Evidence {
		ev.set(item.Field, item.Value, item.Source)
	}
	ev.set("request_field", route.request.primary(), route.request.sourceOr(route.Source))
	ev.set("response_path", route.response.responsePath(), route.response.sourceOr(route.Source))
	port, portNote := choosePort(route, ports)
	if portNote != "" {
		inf.Notes = append(inf.Notes, portNote)
	}
	if port.value == "" {
		inf.Notes = append(inf.Notes, "No AST-proven executable server port call was found; using the default port 8080. Configure the local endpoint manually if it differs.")
	} else {
		ev.set("port", port.value, port.source)
	}
	inf.Evidence = append(inf.Evidence, ev.items...)
}

func (s *dtoShape) sourceOr(fallback string) string {
	if s == nil || s.source == "" {
		return fallback
	}
	return s.source
}

func adapterServiceRoot(path string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for i, part := range parts {
		if (part == "services" || part == "apps" || part == "packages") && i+1 < len(parts) {
			return strings.Join(parts[:i+2], "/")
		}
	}
	for i, part := range parts {
		if part == "src" || part == "test" || part == "tests" {
			if i == 0 {
				return "."
			}
			return strings.Join(parts[:i], "/")
		}
	}
	if len(parts) <= 1 {
		return "."
	}
	return strings.Join(parts[:len(parts)-1], "/")
}

func adapterAgent(root string) string {
	if root == "." {
		return "app"
	}
	return slugIdentifier(filepath.Base(root))
}

func isEvidenceOnlyFile(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(path))
	return strings.HasPrefix(lower, "docs/") ||
		strings.Contains(lower, "/docs/") ||
		strings.HasPrefix(filepath.Base(lower), "readme") ||
		isAdapterTestFile(lower)
}

func isAdapterTestFile(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(path))
	base := filepath.Base(lower)
	return strings.HasPrefix(lower, "test/") ||
		strings.HasPrefix(lower, "tests/") ||
		strings.Contains(lower, "/test/") ||
		strings.Contains(lower, "/tests/") ||
		strings.Contains(base, "_test.") ||
		strings.HasPrefix(base, "test_") ||
		strings.Contains(base, ".test.") ||
		strings.Contains(base, ".spec.")
}

func isOperationalPath(path string) bool {
	segments := strings.Split(strings.Trim(canonicalRoutePath(path), "/"), "/")
	if len(segments) == 0 {
		return false
	}
	switch strings.ToLower(segments[len(segments)-1]) {
	case "health", "metrics", "ready", "docs":
		return true
	default:
		return false
	}
}
