// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	"fmt"
	"path"
	"strings"

	ts "github.com/odvcencio/gotreesitter"
)

type pyField struct {
	name string
	typ  string
}

type pyModel struct {
	name   string
	bases  []string
	fields []pyField
	source string
}

type pyParam struct {
	kind     javaAnnotationKind
	name     string
	variable string
	typ      string
	optional bool
}

type pyRoute struct {
	method   string
	path     string
	source   string
	params   []pyParam
	response string
}

type pyPort struct {
	value  string
	source string
	app    string
	module string
}

// pyFacts is everything inference needs from one Python module.
type pyFacts struct {
	path     string
	imports  map[string]string
	pydantic map[string]bool
	apps     map[string]bool
	models   []pyModel
	routes   []pyRoute
	ports    []pyPort
	notes    []string
}

type pyInstance struct {
	kind   string
	prefix string
	ok     bool
}

func pyFieldChildren(f *syntaxFile, n *ts.Node, name string) []*ts.Node {
	var out []*ts.Node
	for i := 0; i < n.ChildCount(); i++ {
		if n.FieldNameForChild(i, f.lang) == name {
			out = append(out, n.Child(i))
		}
	}
	return out
}

func extractPythonFacts(f *syntaxFile) *pyFacts {
	facts := &pyFacts{path: f.path, imports: map[string]string{}, pydantic: map[string]bool{}, apps: map[string]bool{}}
	fastapiNames := map[string]string{}
	fastapiModules := map[string]bool{}
	uvicornRun := map[string]bool{}
	uvicornModules := map[string]bool{}
	pydanticModules := map[string]bool{}
	for _, statement := range f.named(f.root) {
		switch f.typ(statement) {
		case "import_from_statement":
			module := compactJavaName(f.text(f.field(statement, "module_name")))
			for _, name := range pyFieldChildren(f, statement, "name") {
				original, alias := pyImportName(f, name)
				facts.imports[alias] = original
				switch {
				case (module == "fastapi" || module == "fastapi.applications") && original == "FastAPI",
					(module == "fastapi" || module == "fastapi.routing") && original == "APIRouter":
					fastapiNames[alias] = original
				case module == "fastapi" || module == "fastapi.params":
					fastapiNames[alias] = original
				case module == "pydantic" && original == "BaseModel":
					facts.pydantic[alias] = true
				case module == "uvicorn" && original == "run":
					uvicornRun[alias] = true
				}
			}
		case "import_statement":
			for _, name := range pyFieldChildren(f, statement, "name") {
				original, alias := pyImportName(f, name)
				switch original {
				case "fastapi":
					fastapiModules[alias] = true
				case "uvicorn":
					uvicornModules[alias] = true
				case "pydantic":
					pydanticModules[alias] = true
				}
			}
		}
	}
	// fastapiCallee reports which fastapi export a callee expression names.
	fastapiCallee := func(n *ts.Node) string {
		switch f.typ(n) {
		case "identifier":
			return fastapiNames[f.text(n)]
		case "attribute":
			if object := f.field(n, "object"); f.typ(object) == "identifier" && fastapiModules[f.text(object)] {
				return f.text(f.field(n, "attribute"))
			}
		}
		return ""
	}
	isPydanticBase := func(n *ts.Node) bool {
		switch f.typ(n) {
		case "identifier":
			return facts.pydantic[f.text(n)]
		case "attribute":
			object := f.field(n, "object")
			return f.typ(object) == "identifier" && pydanticModules[f.text(object)] && f.text(f.field(n, "attribute")) == "BaseModel"
		}
		return false
	}

	instances := map[string]*pyInstance{}
	mounts := map[string][]string{}
	for _, statement := range f.named(f.root) {
		switch f.typ(statement) {
		case "expression_statement", "assignment", "call":
			expr := pyStatementExpr(f, statement)
			if f.typ(expr) == "assignment" {
				left, right := f.field(expr, "left"), f.field(expr, "right")
				if f.typ(left) != "identifier" || f.typ(right) != "call" {
					continue
				}
				switch kind := fastapiCallee(f.field(right, "function")); kind {
				case "FastAPI", "APIRouter":
					instance := &pyInstance{kind: kind, ok: true}
					if prefix := pyKeyword(f, right, "prefix"); prefix != nil {
						instance.prefix, instance.ok = f.staticString(prefix)
					}
					instances[f.text(left)] = instance
					if kind == "FastAPI" {
						facts.apps[f.text(left)] = true
					}
				}
				continue
			}
			if f.typ(expr) != "call" {
				continue
			}
			callee := f.field(expr, "function")
			if f.typ(callee) != "attribute" || f.text(f.field(callee, "attribute")) != "include_router" {
				continue
			}
			target := instances[f.text(f.field(callee, "object"))]
			args := pyPositional(f, expr)
			if target == nil || target.kind != "FastAPI" || len(args) == 0 || f.typ(args[0]) != "identifier" {
				continue
			}
			prefix := ""
			if node := pyKeyword(f, expr, "prefix"); node != nil {
				value, ok := f.staticString(node)
				if !ok {
					facts.notes = append(facts.notes, fmt.Sprintf("Skipped unsupported route mount at %s: include_router prefix is not a literal", f.source(expr)))
					continue
				}
				prefix = value
			}
			mounts[f.text(args[0])] = append(mounts[f.text(args[0])], prefix)
		case "class_definition":
			model := pyModel{name: f.text(f.field(statement, "name")), source: f.source(statement)}
			proven := false
			for _, base := range f.named(f.field(statement, "superclasses")) {
				if isPydanticBase(base) {
					proven = true
				} else if f.typ(base) == "identifier" {
					model.bases = append(model.bases, f.text(base))
				}
			}
			if proven {
				model.bases = append(model.bases, "BaseModel")
			}
			model.fields = pyModelFields(f, statement)
			facts.models = append(facts.models, model)
		}
	}

	for _, statement := range f.namedOfType(f.root, "decorated_definition") {
		definition := f.field(statement, "definition")
		if f.typ(definition) != "function_definition" {
			continue
		}
		for _, decorator := range f.namedOfType(statement, "decorator") {
			call := f.firstNamed(decorator, "call")
			callee := f.field(call, "function")
			if f.typ(callee) != "attribute" {
				continue
			}
			instance := instances[f.text(f.field(callee, "object"))]
			method := strings.ToUpper(f.text(f.field(callee, "attribute")))
			if instance == nil || !isRouteVerb(method) {
				continue
			}
			routePath, ok := pyRoutePath(f, call)
			if !ok || !instance.ok {
				facts.notes = append(facts.notes, fmt.Sprintf("Skipped unsupported route at %s: path or router prefix is not a literal", f.source(decorator)))
				continue
			}
			var prefixes []string
			if instance.kind == "FastAPI" {
				prefixes = []string{""}
			} else {
				for _, mount := range mounts[f.text(f.field(callee, "object"))] {
					prefixes = append(prefixes, joinURLPath(mount, instance.prefix))
				}
				if len(prefixes) == 0 {
					facts.notes = append(facts.notes, fmt.Sprintf("Skipped unsupported route at %s: APIRouter %s is not mounted by include_router on a FastAPI app in this module", f.source(decorator), f.text(f.field(callee, "object"))))
					continue
				}
			}
			response := ""
			if model := pyKeyword(f, call, "response_model"); f.typ(model) == "identifier" {
				response = f.text(model)
			} else if returns := f.field(definition, "return_type"); returns != nil {
				response, _ = pyTypeName(f, returns)
			}
			params := pyParams(f, definition, routePath, fastapiNames)
			for _, prefix := range prefixes {
				facts.routes = append(facts.routes, pyRoute{
					method:   method,
					path:     joinURLPath(prefix, routePath),
					source:   f.source(decorator),
					params:   params,
					response: response,
				})
			}
		}
	}

	f.walk(f.root, func(n *ts.Node) bool {
		if f.typ(n) != "call" {
			return true
		}
		callee := f.field(n, "function")
		isRun := (f.typ(callee) == "identifier" && uvicornRun[f.text(callee)]) ||
			(f.typ(callee) == "attribute" && f.text(f.field(callee, "attribute")) == "run" && uvicornModules[f.text(f.field(callee, "object"))])
		if !isRun {
			return true
		}
		args := pyPositional(f, n)
		portNode := pyKeyword(f, n, "port")
		port, ok := f.staticInt(portNode)
		if len(args) == 0 || !ok || !validPort(port) {
			return true
		}
		fact := pyPort{value: fmt.Sprint(port), source: f.source(n)}
		if f.typ(args[0]) == "identifier" && facts.apps[f.text(args[0])] {
			fact.app = f.text(args[0])
		} else if target, ok := f.staticString(args[0]); ok {
			fact.module, fact.app, _ = strings.Cut(target, ":")
		} else {
			return true
		}
		facts.ports = append(facts.ports, fact)
		return true
	})
	return facts
}

// pyStatementExpr returns the expression a simple statement evaluates. The
// grammar emits module-level assignments and calls with or without an
// expression_statement wrapper.
func pyStatementExpr(f *syntaxFile, statement *ts.Node) *ts.Node {
	if f.typ(statement) != "expression_statement" {
		return statement
	}
	if inner := f.named(statement); len(inner) == 1 {
		return inner[0]
	}
	return nil
}

func pyImportName(f *syntaxFile, n *ts.Node) (original, alias string) {
	if f.typ(n) == "aliased_import" {
		return compactJavaName(f.text(f.field(n, "name"))), f.text(f.field(n, "alias"))
	}
	name := compactJavaName(f.text(n))
	return name, name
}

func pyPositional(f *syntaxFile, call *ts.Node) []*ts.Node {
	var out []*ts.Node
	for _, arg := range f.named(f.field(call, "arguments")) {
		switch f.typ(arg) {
		case "keyword_argument", "list_splat", "dictionary_splat":
		default:
			out = append(out, arg)
		}
	}
	return out
}

func pyKeyword(f *syntaxFile, call *ts.Node, name string) *ts.Node {
	for _, arg := range f.namedOfType(f.field(call, "arguments"), "keyword_argument") {
		if f.text(f.field(arg, "name")) == name {
			return f.field(arg, "value")
		}
	}
	return nil
}

func pyRoutePath(f *syntaxFile, call *ts.Node) (string, bool) {
	if node := pyKeyword(f, call, "path"); node != nil {
		return f.staticString(node)
	}
	args := pyPositional(f, call)
	if len(args) == 0 {
		return "", false
	}
	return f.staticString(args[0])
}

// pyTypeName unwraps Optional[...] and X | None and returns the base type
// name plus whether None is allowed.
func pyTypeName(f *syntaxFile, n *ts.Node) (string, bool) {
	if f.typ(n) == "type" {
		inner := f.named(n)
		if len(inner) != 1 {
			return "", false
		}
		n = inner[0]
	}
	switch f.typ(n) {
	case "identifier", "attribute":
		return f.text(n), false
	case "none":
		return "None", true
	case "generic_type", "subscript":
		children := f.named(n)
		if len(children) == 0 {
			return "", false
		}
		base := f.text(children[0])
		params := pyTypeParams(f, n)
		if base == "Optional" && len(params) == 1 {
			name, _ := pyTypeName(f, params[0])
			return name, true
		}
		return base, false
	case "binary_operator", "union_type":
		var names []string
		optional := false
		for _, side := range f.named(n) {
			name, _ := pyTypeName(f, side)
			if name == "None" {
				optional = true
			} else {
				names = append(names, name)
			}
		}
		if len(names) == 1 {
			return names[0], optional
		}
	}
	return "", false
}

func pyTypeParams(f *syntaxFile, n *ts.Node) []*ts.Node {
	var out []*ts.Node
	for _, child := range f.named(n)[1:] {
		switch f.typ(child) {
		case "type_parameter":
			out = append(out, f.named(child)...)
		default:
			out = append(out, child)
		}
	}
	return out
}

func pyModelFields(f *syntaxFile, class *ts.Node) []pyField {
	var fields []pyField
	for _, statement := range f.named(f.field(class, "body")) {
		assignment := pyStatementExpr(f, statement)
		if f.typ(assignment) != "assignment" {
			continue
		}
		typeNode := f.field(assignment, "type")
		left := f.field(assignment, "left")
		if typeNode == nil || f.typ(left) != "identifier" {
			continue
		}
		typ, _ := pyTypeName(f, typeNode)
		if typ == "ClassVar" {
			continue
		}
		name := f.text(left)
		if right := f.field(assignment, "right"); f.typ(right) == "call" && f.text(f.field(right, "function")) == "Field" {
			if alias := pyKeyword(f, right, "alias"); alias != nil {
				value, ok := f.staticString(alias)
				if !ok {
					continue
				}
				name = value
			}
		}
		fields = append(fields, pyField{name: name, typ: typ})
	}
	return fields
}

var pyParamMarkers = map[string]javaAnnotationKind{
	"Query": javaQueryParam, "Path": javaPathParam, "Header": javaHeaderParam,
	"Body": javaBodyParam, "Form": javaBodyParam, "File": javaBodyParam,
	"Depends": -1, "Security": -1, "Cookie": -1,
}

// pyFrameworkTypes are injected by FastAPI/Starlette rather than read from
// the request.
var pyFrameworkTypes = map[string]bool{
	"Request": true, "Response": true, "WebSocket": true, "BackgroundTasks": true,
	"HTTPConnection": true, "SecurityScopes": true,
}

// pyParams classifies handler parameters the way FastAPI does: explicit
// Query/Header/Path/Body markers first, then path-template names, then model
// bodies, then scalar query parameters.
func pyParams(f *syntaxFile, definition *ts.Node, routePath string, fastapiNames map[string]string) []pyParam {
	templateNames := map[string]bool{}
	for _, segment := range strings.Split(routePath, "/") {
		if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
			name, _, _ := strings.Cut(strings.Trim(segment, "{}"), ":")
			templateNames[name] = true
		}
	}
	var params []pyParam
	for _, param := range f.named(f.field(definition, "parameters")) {
		var nameNode, typeNode, defaultNode *ts.Node
		switch f.typ(param) {
		case "identifier":
			nameNode = param
		case "typed_parameter":
			nameNode, typeNode = f.firstNamed(param, "identifier"), f.field(param, "type")
		case "default_parameter":
			nameNode, defaultNode = f.field(param, "name"), f.field(param, "value")
		case "typed_default_parameter":
			nameNode, typeNode, defaultNode = f.field(param, "name"), f.field(param, "type"), f.field(param, "value")
		default:
			continue
		}
		variable := f.text(nameNode)
		out := pyParam{name: variable, variable: variable, optional: defaultNode != nil}
		var marker *ts.Node
		if typeNode != nil {
			inner := f.named(typeNode)
			if len(inner) == 1 && (f.typ(inner[0]) == "generic_type" || f.typ(inner[0]) == "subscript") && f.text(f.named(inner[0])[0]) == "Annotated" {
				typeParams := pyTypeParams(f, inner[0])
				if len(typeParams) > 0 {
					out.typ, _ = pyTypeName(f, typeParams[0])
				}
				for _, extra := range typeParams[1:] {
					if call := f.firstNamed(extra, "call"); call != nil {
						marker = call
					} else if f.typ(extra) == "call" {
						marker = extra
					}
				}
			} else {
				var nullable bool
				out.typ, nullable = pyTypeName(f, typeNode)
				out.optional = out.optional || nullable
			}
		}
		if f.typ(defaultNode) == "call" && pyParamMarkers[fastapiNames[f.text(f.field(defaultNode, "function"))]] != 0 {
			marker = defaultNode
			positional := pyPositional(f, defaultNode)
			out.optional = (len(positional) > 0 && f.typ(positional[0]) != "ellipsis") || pyKeyword(f, defaultNode, "default") != nil
		}
		if marker != nil {
			out.kind = pyParamMarkers[fastapiNames[f.text(f.field(marker, "function"))]]
			if alias := pyKeyword(f, marker, "alias"); alias != nil {
				if value, ok := f.staticString(alias); ok {
					out.name = value
				}
			} else if out.kind == javaHeaderParam {
				out.name = strings.ReplaceAll(variable, "_", "-")
			}
		}
		if out.kind == 0 && templateNames[variable] {
			out.kind = javaPathParam
		}
		if pyFrameworkTypes[out.typ] || fastapiNames[out.typ] != "" || strings.HasPrefix(out.typ, "fastapi.") {
			continue
		}
		params = append(params, out)
	}
	return params
}

// pyService resolves Pydantic models visible to one service.
type pyService struct {
	models map[string][]pyModel
	proven map[string]bool
}

func newPyService(files []*pyFacts) *pyService {
	svc := &pyService{models: map[string][]pyModel{}, proven: map[string]bool{}}
	for _, file := range files {
		for _, model := range file.models {
			svc.models[model.name] = append(svc.models[model.name], model)
		}
	}
	for changed := true; changed; {
		changed = false
		for name, models := range svc.models {
			if svc.proven[name] || len(models) != 1 {
				continue
			}
			for _, base := range models[0].bases {
				if base == "BaseModel" || svc.proven[base] {
					svc.proven[name], changed = true, true
				}
			}
		}
	}
	return svc
}

func (s *pyService) model(name string) (pyModel, bool) {
	models := s.models[name]
	if len(models) != 1 || !s.proven[name] {
		return pyModel{}, false
	}
	return models[0], true
}

func (s *pyService) shape(name string, depth int) *dtoShape {
	model, ok := s.model(name)
	if !ok || depth > 3 {
		return nil
	}
	shape := &dtoShape{source: model.source}
	for _, field := range model.fields {
		item := dtoShapeField{name: field.name, isString: field.typ == "str"}
		if !item.isString {
			item.nested = s.shape(field.typ, depth+1)
		}
		shape.fields = append(shape.fields, item)
	}
	return shape
}

func pythonServiceCandidates(root string, files []*pyFacts) ([]RouteCandidate, []portEvidence) {
	svc := newPyService(files)
	var out []RouteCandidate
	for _, file := range files {
		for _, route := range file.routes {
			candidate := newAdapterRoute("python", root, route.method, route.path, route.source)
			var headers []string
			var bodies []pyParam
			for _, param := range route.params {
				switch {
				case param.kind == javaPathParam:
					candidate.PathParams = append(candidate.PathParams, param.name)
					candidate.PathMappings = append(candidate.PathMappings, param.name+":"+param.variable)
				case param.kind == javaHeaderParam:
					headers = append(headers, param.name)
				case param.kind == javaBodyParam:
					bodies = append(bodies, param)
				case param.kind == javaQueryParam:
					if !param.optional {
						candidate.QueryParams = append(candidate.QueryParams, param.name)
						candidate.QueryMappings = append(candidate.QueryMappings, param.name+":"+param.variable)
					}
				case param.kind == 0:
					if _, ok := svc.model(param.typ); ok {
						bodies = append(bodies, param)
					} else if !param.optional {
						candidate.QueryParams = append(candidate.QueryParams, param.name)
						candidate.QueryMappings = append(candidate.QueryMappings, param.name+":"+param.variable)
					}
				}
			}
			candidate.Header = chooseRouteHeader(headers)
			if len(bodies) == 1 {
				candidate.request = svc.shape(bodies[0].typ, 0)
			}
			candidate.response = svc.shape(route.response, 0)
			out = append(out, candidate)
		}
	}
	var ports []portEvidence
	apps := map[string]map[string]bool{}
	for _, file := range files {
		apps[strings.TrimSuffix(path.Base(file.path), ".py")] = file.apps
	}
	for _, file := range files {
		for _, port := range file.ports {
			if port.module != "" {
				segments := strings.Split(port.module, ".")
				if !apps[segments[len(segments)-1]][port.app] {
					continue
				}
			}
			ports = append(ports, portEvidence{value: port.value, source: port.source})
		}
	}
	return out, ports
}

// extractPythonTestCalls follows statements in order so assertions bind to
// the request most recently assigned to the variable they read. Only a
// FastAPI TestClient or AsyncClient bound to an app is allowed as a client.
func extractPythonTestCalls(f *syntaxFile) []testCallFact {
	clients := pythonTestClients(f)
	var calls []*testCallFact
	var visit func(block *ts.Node, parentEnv map[string]string)
	visit = func(block *ts.Node, parentEnv map[string]string) {
		env := map[string]string{}
		for key, value := range parentEnv {
			env[key] = value
		}
		bound := map[string]*testCallFact{}
		for _, statement := range f.named(block) {
			switch f.typ(statement) {
			case "expression_statement", "assignment", "call", "await":
				expr := []*ts.Node{pyStatementExpr(f, statement)}
				if expr[0] == nil {
					continue
				}
				if f.typ(expr[0]) == "assignment" {
					left, right := f.field(expr[0], "left"), pyUnwrapAwait(f, f.field(expr[0], "right"))
					name := f.text(left)
					delete(bound, name)
					delete(env, name)
					if call := pythonRequestCall(f, right, env, clients); call != nil {
						calls = append(calls, call)
						bound[name] = call
					} else if envName := pyEnvName(f, right, env); envName != "" {
						env[name] = envName
					}
					continue
				}
				if call := pythonRequestCall(f, pyUnwrapAwait(f, expr[0]), env, clients); call != nil {
					calls = append(calls, call)
				}
			case "assert_statement":
				for _, expr := range f.named(statement) {
					pythonAssertion(f, expr, bound, env, clients, &calls)
				}
			default:
				for _, nested := range pyNestedBlocks(f, statement) {
					visit(nested, env)
				}
			}
		}
	}
	visit(f.root, nil)
	out := make([]testCallFact, 0, len(calls))
	for _, call := range calls {
		out = append(out, *call)
	}
	return out
}

func pythonTestClients(f *syntaxFile) map[string]bool {
	testClients := map[string]bool{}
	apps := map[string]bool{}
	constructors := map[string]bool{}
	for _, statement := range f.named(f.root) {
		if f.typ(statement) != "import_from_statement" {
			continue
		}
		module := compactJavaName(f.text(f.field(statement, "module_name")))
		for _, name := range pyFieldChildren(f, statement, "name") {
			original, alias := pyImportName(f, name)
			switch {
			case module == "fastapi.testclient" && original == "TestClient":
				constructors[alias] = true
			case module == "httpx" && original == "AsyncClient":
				constructors[alias] = true
			case original == "app":
				apps[alias] = true
			}
		}
	}
	for _, statement := range f.named(f.root) {
		expr := pyStatementExpr(f, statement)
		if f.typ(expr) != "assignment" {
			continue
		}
		left, right := f.field(expr, "left"), f.field(expr, "right")
		if f.typ(left) != "identifier" || f.typ(right) != "call" || !constructors[f.text(f.field(right, "function"))] {
			continue
		}
		var app *ts.Node
		if args := pyPositional(f, right); len(args) > 0 {
			app = args[0]
		} else {
			app = pyKeyword(f, right, "app")
		}
		if f.typ(app) == "identifier" && apps[f.text(app)] {
			testClients[f.text(left)] = true
		}
	}
	return testClients
}

func pyNestedBlocks(f *syntaxFile, n *ts.Node) []*ts.Node {
	var blocks []*ts.Node
	f.walk(n, func(child *ts.Node) bool {
		if f.typ(child) == "block" {
			blocks = append(blocks, child)
			return false
		}
		return true
	})
	return blocks
}

func pyUnwrapAwait(f *syntaxFile, n *ts.Node) *ts.Node {
	if f.typ(n) == "await" {
		if inner := f.named(n); len(inner) == 1 {
			return inner[0]
		}
	}
	return n
}

func pythonRequestCall(f *syntaxFile, n *ts.Node, env map[string]string, clients map[string]bool) *testCallFact {
	if f.typ(n) != "call" {
		return nil
	}
	callee := f.field(n, "function")
	if f.typ(callee) != "attribute" {
		return nil
	}
	if object := f.field(callee, "object"); f.typ(object) != "identifier" || !clients[f.text(object)] {
		return nil
	}
	method := strings.ToUpper(f.text(f.field(callee, "attribute")))
	args := pyPositional(f, n)
	if !isRouteVerb(method) || len(args) == 0 {
		return nil
	}
	routePath, ok := f.staticString(args[0])
	if !ok || !(strings.HasPrefix(routePath, "/") || strings.Contains(routePath, "://")) {
		return nil
	}
	call := &testCallFact{route: canonicalRouteIdentity(method, routePath), source: f.source(n)}
	if headers := pyKeyword(f, n, "headers"); f.typ(headers) == "dictionary" {
		for _, pair := range f.namedOfType(headers, "pair") {
			name, ok := f.staticString(f.field(pair, "key"))
			if !ok {
				continue
			}
			call.headers = append(call.headers, testHeader{name: name, env: pyEnvName(f, f.field(pair, "value"), env), source: f.source(pair)})
		}
	}
	if body := pyKeyword(f, n, "json"); f.typ(body) == "dictionary" {
		call.bodyStatic = true
		for _, pair := range f.namedOfType(body, "pair") {
			key, ok := f.staticString(f.field(pair, "key"))
			if !ok {
				call.bodyStatic = false
				continue
			}
			value, ok := f.staticString(f.field(pair, "value"))
			call.body = append(call.body, keyValue{key: key, value: value, static: ok})
		}
	}
	return call
}

func pyEnvName(f *syntaxFile, n *ts.Node, env map[string]string) string {
	switch f.typ(n) {
	case "identifier":
		return env[f.text(n)]
	case "subscript":
		if compactJavaName(f.text(f.field(n, "value"))) == "os.environ" {
			name, _ := f.staticString(f.field(n, "subscript"))
			return name
		}
	case "call":
		switch compactJavaName(f.text(f.field(n, "function"))) {
		case "os.getenv", "os.environ.get", "getenv", "environ.get":
			if args := pyPositional(f, n); len(args) > 0 {
				name, _ := f.staticString(args[0])
				return name
			}
		}
	}
	return ""
}

// pythonAssertion binds `<response>.json()[k1][k2] == "literal"` to the call
// the response variable currently refers to.
func pythonAssertion(f *syntaxFile, n *ts.Node, bound map[string]*testCallFact, env map[string]string, clients map[string]bool, calls *[]*testCallFact) {
	if f.typ(n) != "comparison_operator" {
		return
	}
	sides := f.named(n)
	if len(sides) != 2 || strings.TrimSpace(string(f.src[sides[0].EndByte():sides[1].StartByte()])) != "==" {
		return
	}
	for i, side := range sides {
		value, ok := f.staticString(sides[1-i])
		if !ok {
			continue
		}
		var keys []string
		for f.typ(side) == "subscript" {
			key, ok := f.staticString(f.field(side, "subscript"))
			if !ok {
				return
			}
			keys = append([]string{key}, keys...)
			side = f.field(side, "value")
		}
		if f.typ(side) != "call" || len(keys) == 0 {
			continue
		}
		callee := f.field(side, "function")
		if f.typ(callee) != "attribute" || f.text(f.field(callee, "attribute")) != "json" {
			continue
		}
		target := f.field(callee, "object")
		call := bound[f.text(target)]
		if f.typ(target) != "identifier" {
			if call = pythonRequestCall(f, target, env, clients); call != nil {
				*calls = append(*calls, call)
			}
		}
		if call != nil {
			call.assertions = append(call.assertions, testAssertion{path: strings.Join(keys, "."), value: value, source: f.source(n)})
		}
		return
	}
}
