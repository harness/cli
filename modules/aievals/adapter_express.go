// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	"fmt"
	"path"
	"strings"

	ts "github.com/odvcencio/gotreesitter"
)

type tsField struct {
	name     string
	isString bool
	ref      string
	inline   *tsShapeDecl
}

type tsShapeDecl struct {
	fields []tsField
	source string
}

type tsTypeRef struct {
	ref    string
	inline *tsShapeDecl
}

type tsRoute struct {
	method   string
	path     string
	source   string
	request  tsTypeRef
	response tsTypeRef
}

type tsImport struct {
	module string
	name   string
}

// tsFacts is everything inference needs from one JavaScript/TypeScript file.
type tsFacts struct {
	path    string
	shapes  map[string]*tsShapeDecl
	imports map[string]tsImport
	routes  []tsRoute
	ports   []portEvidence
	notes   []string
}

type expressBindings struct {
	defaults map[string]bool
	named    map[string]string
	apps     map[string]bool
	routers  map[string]bool
}

func (b expressBindings) isExpressExport(f *syntaxFile, n *ts.Node, export string) bool {
	switch f.typ(n) {
	case "identifier", "type_identifier":
		return b.named[f.text(n)] == export
	case "member_expression":
		return b.defaults[f.text(f.field(n, "object"))] && f.text(f.field(n, "property")) == export
	case "nested_type_identifier":
		return b.defaults[f.text(f.field(n, "module"))] && f.text(f.field(n, "name")) == export
	}
	return false
}

func jsTopLevelDeclarators(f *syntaxFile) []*ts.Node {
	var out []*ts.Node
	for _, statement := range f.named(f.root) {
		if f.typ(statement) == "export_statement" {
			if declaration := f.field(statement, "declaration"); declaration != nil {
				statement = declaration
			}
		}
		switch f.typ(statement) {
		case "lexical_declaration", "variable_declaration":
			out = append(out, f.namedOfType(statement, "variable_declarator")...)
		}
	}
	return out
}

func jsRequireOf(f *syntaxFile, n *ts.Node) (string, bool) {
	if f.typ(n) != "call_expression" || f.text(f.field(n, "function")) != "require" {
		return "", false
	}
	args := f.named(f.field(n, "arguments"))
	if len(args) != 1 {
		return "", false
	}
	return f.staticString(args[0])
}

func extractExpressFacts(f *syntaxFile) *tsFacts {
	facts := &tsFacts{path: f.path, shapes: map[string]*tsShapeDecl{}, imports: map[string]tsImport{}}
	bindings := expressBindings{defaults: map[string]bool{}, named: map[string]string{}, apps: map[string]bool{}, routers: map[string]bool{}}
	for _, statement := range f.namedOfType(f.root, "import_statement") {
		module, ok := f.staticString(f.field(statement, "source"))
		if !ok {
			continue
		}
		clause := f.firstNamed(statement, "import_clause")
		for _, part := range f.named(clause) {
			switch f.typ(part) {
			case "identifier":
				facts.imports[f.text(part)] = tsImport{module: module, name: "default"}
				if module == "express" {
					bindings.defaults[f.text(part)] = true
				}
			case "namespace_import":
				if module == "express" {
					if alias := f.firstNamed(part, "identifier"); alias != nil {
						bindings.defaults[f.text(alias)] = true
					}
				}
			case "named_imports":
				for _, specifier := range f.namedOfType(part, "import_specifier") {
					name := f.text(f.field(specifier, "name"))
					alias := name
					if node := f.field(specifier, "alias"); node != nil {
						alias = f.text(node)
					}
					facts.imports[alias] = tsImport{module: module, name: name}
					if module == "express" {
						bindings.named[alias] = name
					}
				}
			}
		}
	}
	declarators := jsTopLevelDeclarators(f)
	for _, declarator := range declarators {
		name, value := f.field(declarator, "name"), f.field(declarator, "value")
		if module, ok := jsRequireOf(f, value); ok && module == "express" {
			switch f.typ(name) {
			case "identifier":
				bindings.defaults[f.text(name)] = true
			case "object_pattern":
				for _, prop := range f.named(name) {
					switch f.typ(prop) {
					case "shorthand_property_identifier_pattern":
						bindings.named[f.text(prop)] = f.text(prop)
					case "pair_pattern":
						bindings.named[f.text(f.field(prop, "value"))] = f.text(f.field(prop, "key"))
					}
				}
			}
		}
	}
	consts := map[string]int{}
	for _, declarator := range declarators {
		name, value := f.field(declarator, "name"), f.field(declarator, "value")
		if f.typ(name) != "identifier" {
			continue
		}
		if port, ok := f.staticInt(value); ok {
			consts[f.text(name)] = port
		}
		if f.typ(value) != "call_expression" {
			continue
		}
		callee := f.field(value, "function")
		switch {
		case f.typ(callee) == "identifier" && bindings.defaults[f.text(callee)]:
			bindings.apps[f.text(name)] = true
		case bindings.isExpressExport(f, callee, "Router"):
			bindings.routers[f.text(name)] = true
		}
	}
	for _, statement := range f.named(f.root) {
		if f.typ(statement) == "export_statement" {
			if declaration := f.field(statement, "declaration"); declaration != nil {
				statement = declaration
			}
		}
		switch f.typ(statement) {
		case "interface_declaration":
			facts.shapes[f.text(f.field(statement, "name"))] = tsObjectShape(f, f.field(statement, "body"), statement)
		case "type_alias_declaration":
			if value := f.field(statement, "value"); f.typ(value) == "object_type" {
				facts.shapes[f.text(f.field(statement, "name"))] = tsObjectShape(f, value, statement)
			}
		}
	}

	mounts := map[string][]string{}
	type pendingRoute struct {
		owner string
		route tsRoute
		node  *ts.Node
	}
	var pending []pendingRoute
	for _, statement := range f.namedOfType(f.root, "expression_statement") {
		call := f.firstNamed(statement, "call_expression")
		callee := f.field(call, "function")
		if f.typ(callee) != "member_expression" {
			continue
		}
		owner := f.text(f.field(callee, "object"))
		property := f.text(f.field(callee, "property"))
		args := f.named(f.field(call, "arguments"))
		if !bindings.apps[owner] && !bindings.routers[owner] {
			continue
		}
		if property == "use" && bindings.apps[owner] {
			prefix, rest := "", args
			if len(args) > 0 {
				if value, ok := f.staticString(args[0]); ok {
					prefix, rest = value, args[1:]
				}
			}
			for _, arg := range rest {
				if f.typ(arg) == "identifier" && bindings.routers[f.text(arg)] {
					mounts[f.text(arg)] = append(mounts[f.text(arg)], prefix)
				}
			}
			continue
		}
		if property == "listen" && bindings.apps[owner] && len(args) > 0 {
			port, ok := f.staticInt(args[0])
			if !ok && f.typ(args[0]) == "identifier" {
				port, ok = consts[f.text(args[0])]
			}
			if ok && validPort(port) {
				facts.ports = append(facts.ports, portEvidence{value: fmt.Sprint(port), source: f.source(call)})
			}
			continue
		}
		method := strings.ToUpper(property)
		if !isRouteVerb(method) || len(args) == 0 {
			continue
		}
		routePath, ok := f.staticString(args[0])
		if !ok {
			facts.notes = append(facts.notes, fmt.Sprintf("Skipped unsupported route at %s: path is not a literal", f.source(call)))
			continue
		}
		route := tsRoute{method: method, path: routePath, source: f.source(call)}
		route.request, route.response = expressHandlerTypes(f, bindings, args[len(args)-1])
		pending = append(pending, pendingRoute{owner: owner, route: route, node: call})
	}
	for _, item := range pending {
		if bindings.apps[item.owner] {
			facts.routes = append(facts.routes, item.route)
			continue
		}
		if len(mounts[item.owner]) == 0 {
			facts.notes = append(facts.notes, fmt.Sprintf("Skipped unsupported route at %s: router %s is not mounted with use() on an express() app in this module", f.source(item.node), item.owner))
			continue
		}
		for _, prefix := range mounts[item.owner] {
			route := item.route
			route.path = joinURLPath(prefix, route.path)
			facts.routes = append(facts.routes, route)
		}
	}
	return facts
}

func tsObjectShape(f *syntaxFile, body, owner *ts.Node) *tsShapeDecl {
	shape := &tsShapeDecl{source: f.source(owner)}
	for _, prop := range f.namedOfType(body, "property_signature") {
		field := tsField{name: f.text(f.field(prop, "name"))}
		if annotation := f.field(prop, "type"); annotation != nil {
			typ := f.firstNamedAny(annotation)
			if typ == nil {
				typ = annotation
			}
			switch f.typ(typ) {
			case "predefined_type":
				field.isString = f.text(typ) == "string"
			case "type_identifier":
				field.ref = f.text(typ)
			case "object_type":
				field.inline = tsObjectShape(f, typ, owner)
			case "literal_type":
				field.isString = f.firstNamed(typ, "string") != nil
			}
		}
		if name, ok := f.staticString(f.field(prop, "name")); ok {
			field.name = name
		}
		shape.fields = append(shape.fields, field)
	}
	return shape
}

func (f *syntaxFile) firstNamedAny(n *ts.Node) *ts.Node {
	if children := f.named(n); len(children) > 0 {
		return children[0]
	}
	return nil
}

func tsTypeRefOf(f *syntaxFile, n *ts.Node) tsTypeRef {
	switch f.typ(n) {
	case "type_identifier":
		return tsTypeRef{ref: f.text(n)}
	case "object_type":
		return tsTypeRef{inline: tsObjectShape(f, n, n)}
	}
	return tsTypeRef{}
}

// expressHandlerTypes reads ReqBody/ResBody from express Request<P, ResBody,
// ReqBody> and Response<ResBody> annotations on the route handler.
func expressHandlerTypes(f *syntaxFile, bindings expressBindings, handler *ts.Node) (tsTypeRef, tsTypeRef) {
	switch f.typ(handler) {
	case "arrow_function", "function_expression", "function":
	default:
		return tsTypeRef{}, tsTypeRef{}
	}
	var request, response tsTypeRef
	params := f.namedOfType(f.field(handler, "parameters"), "required_parameter", "optional_parameter")
	for i, param := range params {
		annotation := f.field(param, "type")
		typ := f.firstNamedAny(annotation)
		if f.typ(typ) != "generic_type" {
			continue
		}
		args := f.named(f.field(typ, "type_arguments"))
		name := f.field(typ, "name")
		switch {
		case i == 0 && bindings.isExpressExport(f, name, "Request"):
			if len(args) >= 2 {
				response = tsTypeRefOf(f, args[1])
			}
			if len(args) >= 3 {
				request = tsTypeRefOf(f, args[2])
			}
		case i == 1 && bindings.isExpressExport(f, name, "Response") && len(args) >= 1:
			response = tsTypeRefOf(f, args[0])
		}
	}
	return request, response
}

// tsService resolves interface and type-alias names across one service.
type tsService struct {
	files map[string]*tsFacts
}

func (s *tsService) lookup(file *tsFacts, name string) (*tsFacts, *tsShapeDecl) {
	if shape := file.shapes[name]; shape != nil {
		return file, shape
	}
	imp, ok := file.imports[name]
	if !ok || !strings.HasPrefix(imp.module, ".") || imp.name == "default" {
		return nil, nil
	}
	base := path.Join(path.Dir(file.path), imp.module)
	for _, candidate := range []string{base + ".ts", base + ".tsx", base + ".d.ts", base + "/index.ts", base + ".js"} {
		if target := s.files[candidate]; target != nil {
			if shape := target.shapes[imp.name]; shape != nil {
				return target, shape
			}
		}
	}
	return nil, nil
}

func (s *tsService) shape(file *tsFacts, ref tsTypeRef, depth int) *dtoShape {
	if depth > 3 {
		return nil
	}
	decl, owner := ref.inline, file
	if decl == nil && ref.ref != "" {
		owner, decl = s.lookup(file, ref.ref)
	}
	if decl == nil {
		return nil
	}
	shape := &dtoShape{source: decl.source}
	for _, field := range decl.fields {
		item := dtoShapeField{name: field.name, isString: field.isString}
		if !item.isString {
			item.nested = s.shape(owner, tsTypeRef{ref: field.ref, inline: field.inline}, depth+1)
		}
		shape.fields = append(shape.fields, item)
	}
	return shape
}

func expressServiceCandidates(root string, files []*tsFacts) ([]RouteCandidate, []portEvidence) {
	svc := &tsService{files: map[string]*tsFacts{}}
	for _, file := range files {
		svc.files[file.path] = file
	}
	var out []RouteCandidate
	var ports []portEvidence
	for _, file := range files {
		adapter := "typescript"
		if sourceKindFor(file.path) == sourceJavaScript {
			adapter = "javascript"
		}
		for _, route := range file.routes {
			candidate := newAdapterRoute(adapter, root, route.method, route.path, route.source)
			for _, segment := range strings.Split(route.path, "/") {
				if strings.HasPrefix(segment, ":") && len(segment) > 1 {
					name := strings.TrimSuffix(strings.TrimPrefix(segment, ":"), "?")
					if !strings.HasSuffix(segment, "?") {
						candidate.PathParams = append(candidate.PathParams, name)
						candidate.PathMappings = append(candidate.PathMappings, name+":"+name)
					}
				}
			}
			candidate.request = svc.shape(file, route.request, 0)
			candidate.response = svc.shape(file, route.response, 0)
			out = append(out, candidate)
		}
		ports = append(ports, file.ports...)
	}
	return out, ports
}

// extractExpressTestCalls reads supertest-style chains:
// request(app).post("/x").set(...).send({...}).expect({...}), plus jest
// expect(res.body.x).toBe("v") on a response bound with const res = await ....
// The request factory and its app binding must be proven, so generic x.post
// chains cannot provide headers, bodies, or expected values for staging.
func extractExpressTestCalls(f *syntaxFile) []testCallFact {
	appNames, requestNames := expressTestBindings(f)
	clientNames := map[string]bool{}
	for _, declarator := range jsTopLevelDeclarators(f) {
		name, value := f.field(declarator, "name"), f.field(declarator, "value")
		if f.typ(name) != "identifier" || f.typ(value) != "call_expression" || !supertestAppCall(f, value, appNames, requestNames) {
			continue
		}
		clientNames[f.text(name)] = true
	}
	type binding struct {
		name  string
		scope *ts.Node
		start uint32
		call  int
	}
	var calls []testCallFact
	var bindings []binding
	f.walk(f.root, func(n *ts.Node) bool {
		if f.typ(n) != "call_expression" {
			return true
		}
		callee := f.field(n, "function")
		if f.typ(callee) != "member_expression" {
			return true
		}
		method := strings.ToUpper(f.text(f.field(callee, "property")))
		object := f.field(callee, "object")
		if !isRouteVerb(method) || !expressTestRequest(f, object, appNames, requestNames, clientNames) {
			return true
		}
		args := f.named(f.field(n, "arguments"))
		if len(args) == 0 {
			return true
		}
		for _, arg := range args {
			switch f.typ(arg) {
			case "arrow_function", "function_expression", "function":
				return true
			}
		}
		routePath, ok := f.staticString(args[0])
		if !ok || !(strings.HasPrefix(routePath, "/") || strings.Contains(routePath, "://")) {
			return true
		}
		call := testCallFact{route: canonicalRouteIdentity(method, routePath), source: f.source(n)}
		current := n
		for {
			parent := jsChainedCall(f, current)
			if parent == nil {
				break
			}
			current = parent
			chained := f.named(f.field(parent, "arguments"))
			switch f.text(f.field(f.field(parent, "function"), "property")) {
			case "set":
				if len(chained) == 2 {
					name, _ := f.staticString(chained[0])
					call.headers = append(call.headers, testHeader{name: name, env: jsEnvName(f, parent, chained[1]), source: f.source(parent)})
				} else if len(chained) == 1 && f.typ(chained[0]) == "object" {
					for _, pair := range f.namedOfType(chained[0], "pair") {
						call.headers = append(call.headers, testHeader{name: jsKey(f, pair), env: jsEnvName(f, parent, f.field(pair, "value")), source: f.source(pair)})
					}
				}
			case "send":
				if len(chained) == 1 && f.typ(chained[0]) == "object" {
					call.body, call.bodyStatic = jsObjectStrings(f, chained[0])
				}
			case "expect":
				for _, arg := range chained {
					if f.typ(arg) == "object" {
						call.assertions = append(call.assertions, jsObjectAssertions(f, arg, "")...)
					}
				}
			}
		}
		top := current
		if parent := top.Parent(); f.typ(parent) == "await_expression" {
			top = parent
		}
		if declarator := top.Parent(); f.typ(declarator) == "variable_declarator" && sameNode(f.field(declarator, "value"), top) && f.typ(f.field(declarator, "name")) == "identifier" {
			bindings = append(bindings, binding{name: f.text(f.field(declarator, "name")), scope: jsScope(f, declarator), start: declarator.StartByte(), call: len(calls)})
		}
		calls = append(calls, call)
		return true
	})
	f.walk(f.root, func(n *ts.Node) bool {
		if f.typ(n) != "call_expression" {
			return true
		}
		matcher := f.field(n, "function")
		if f.typ(matcher) != "member_expression" {
			return true
		}
		switch f.text(f.field(matcher, "property")) {
		case "toBe", "toEqual", "toStrictEqual":
		default:
			return true
		}
		expectCall := f.field(matcher, "object")
		values := f.named(f.field(n, "arguments"))
		if f.typ(expectCall) != "call_expression" || f.text(f.field(expectCall, "function")) != "expect" || len(values) != 1 {
			return true
		}
		value, ok := f.staticString(values[0])
		subjects := f.named(f.field(expectCall, "arguments"))
		if !ok || len(subjects) != 1 {
			return true
		}
		var keys []string
		subject := subjects[0]
		for f.typ(subject) == "member_expression" {
			keys = append([]string{f.text(f.field(subject, "property"))}, keys...)
			subject = f.field(subject, "object")
		}
		if f.typ(subject) != "identifier" || len(keys) < 2 || keys[0] != "body" {
			return true
		}
		best := -1
		for i, bound := range bindings {
			if bound.name == f.text(subject) && bound.start < n.StartByte() && bound.scope.StartByte() <= n.StartByte() && n.EndByte() <= bound.scope.EndByte() {
				if best < 0 || bound.start > bindings[best].start {
					best = i
				}
			}
		}
		if best >= 0 && validResponsePath(strings.Join(keys[1:], ".")) {
			index := bindings[best].call
			calls[index].assertions = append(calls[index].assertions, testAssertion{path: strings.Join(keys[1:], "."), value: value, source: f.source(n)})
		}
		return true
	})
	return calls
}

func expressTestBindings(f *syntaxFile) (map[string]bool, map[string]bool) {
	apps, requests, expressFactories := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, statement := range f.namedOfType(f.root, "import_statement") {
		module, ok := f.staticString(f.field(statement, "source"))
		if !ok {
			continue
		}
		clause := f.firstNamed(statement, "import_clause")
		for _, part := range f.named(clause) {
			switch f.typ(part) {
			case "identifier":
				switch module {
				case "express":
					expressFactories[f.text(part)] = true
				case "supertest":
					requests[f.text(part)] = true
				default:
					if strings.HasPrefix(module, ".") {
						apps[f.text(part)] = true
					}
				}
			case "namespace_import":
				if module == "express" {
					if alias := f.firstNamed(part, "identifier"); alias != nil {
						expressFactories[f.text(alias)] = true
					}
				}
			}
		}
	}
	for _, declarator := range jsTopLevelDeclarators(f) {
		name, value := f.field(declarator, "name"), f.field(declarator, "value")
		if f.typ(name) != "identifier" || f.typ(value) != "call_expression" {
			continue
		}
		if module, ok := jsRequireOf(f, value); ok {
			switch module {
			case "express":
				expressFactories[f.text(name)] = true
			case "supertest":
				requests[f.text(name)] = true
			}
			continue
		}
		if f.typ(f.field(value, "function")) == "identifier" && expressFactories[f.text(f.field(value, "function"))] {
			apps[f.text(name)] = true
		}
	}
	return apps, requests
}

func supertestAppCall(f *syntaxFile, n *ts.Node, apps, requests map[string]bool) bool {
	if f.typ(n) != "call_expression" || f.typ(f.field(n, "function")) != "identifier" || !requests[f.text(f.field(n, "function"))] {
		return false
	}
	args := f.named(f.field(n, "arguments"))
	return len(args) == 1 && f.typ(args[0]) == "identifier" && apps[f.text(args[0])]
}

func expressTestRequest(f *syntaxFile, n *ts.Node, apps, requests, clients map[string]bool) bool {
	if f.typ(n) == "identifier" {
		return clients[f.text(n)]
	}
	return supertestAppCall(f, n, apps, requests)
}

func jsChainedCall(f *syntaxFile, n *ts.Node) *ts.Node {
	member := n.Parent()
	if f.typ(member) != "member_expression" || !sameNode(f.field(member, "object"), n) {
		return nil
	}
	call := member.Parent()
	if f.typ(call) != "call_expression" || !sameNode(f.field(call, "function"), member) {
		return nil
	}
	return call
}

func jsScope(f *syntaxFile, n *ts.Node) *ts.Node {
	for scope := n.Parent(); scope != nil; scope = scope.Parent() {
		switch f.typ(scope) {
		case "statement_block", "program":
			return scope
		}
	}
	return f.root
}

func jsKey(f *syntaxFile, pair *ts.Node) string {
	key := f.field(pair, "key")
	if value, ok := f.staticString(key); ok {
		return value
	}
	if f.typ(key) == "property_identifier" {
		return f.text(key)
	}
	return ""
}

func jsObjectStrings(f *syntaxFile, object *ts.Node) ([]keyValue, bool) {
	var out []keyValue
	static := true
	for _, child := range f.named(object) {
		if f.typ(child) != "pair" {
			static = false
			continue
		}
		key := jsKey(f, child)
		if key == "" {
			static = false
			continue
		}
		value, ok := f.staticString(f.field(child, "value"))
		out = append(out, keyValue{key: key, value: value, static: ok})
	}
	return out, static
}

func jsObjectAssertions(f *syntaxFile, object *ts.Node, prefix string) []testAssertion {
	var out []testAssertion
	for _, pair := range f.namedOfType(object, "pair") {
		key := jsKey(f, pair)
		if key == "" {
			continue
		}
		full := key
		if prefix != "" {
			full = prefix + "." + key
		}
		value := f.field(pair, "value")
		if f.typ(value) == "object" {
			out = append(out, jsObjectAssertions(f, value, full)...)
		} else if literal, ok := f.staticString(value); ok && validResponsePath(full) {
			out = append(out, testAssertion{path: full, value: literal, source: f.source(pair)})
		}
	}
	return out
}

// jsEnvName accepts process.env.X, process.env["X"], or an identifier whose
// nearest preceding declaration in scope is one of those.
func jsEnvName(f *syntaxFile, at, n *ts.Node) string {
	switch f.typ(n) {
	case "member_expression":
		if compactJavaName(f.text(f.field(n, "object"))) == "process.env" {
			return f.text(f.field(n, "property"))
		}
	case "subscript_expression":
		if compactJavaName(f.text(f.field(n, "object"))) == "process.env" {
			name, _ := f.staticString(f.field(n, "index"))
			return name
		}
	case "identifier":
		name := f.text(n)
		for scope := at.Parent(); scope != nil; scope = scope.Parent() {
			if f.typ(scope) != "statement_block" && f.typ(scope) != "program" {
				continue
			}
			var found *ts.Node
			for _, statement := range f.namedOfType(scope, "lexical_declaration", "variable_declaration") {
				if statement.StartByte() >= at.StartByte() {
					break
				}
				for _, declarator := range f.namedOfType(statement, "variable_declarator") {
					if f.text(f.field(declarator, "name")) == name {
						found = f.field(declarator, "value")
					}
				}
			}
			if found != nil {
				return jsEnvName(f, found, found)
			}
		}
	}
	return ""
}
