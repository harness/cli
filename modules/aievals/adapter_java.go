// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	"fmt"
	"strings"

	ts "github.com/odvcencio/gotreesitter"
)

type javaAnnotationKind int

const (
	javaRequestMapping javaAnnotationKind = iota + 1
	javaVerb
	javaJaxPath
	javaMicronautController
	javaQueryParam
	javaPathParam
	javaHeaderParam
	javaBodyParam
	javaDefaultValue
)

type javaAnnotationSpec struct {
	framework string
	kind      javaAnnotationKind
	method    string
}

// javaAnnotations maps fully qualified framework annotations to their route
// roles. An annotation only counts after its simple name resolves to one of
// these through an import or a qualified reference.
var javaAnnotations = func() map[string]javaAnnotationSpec {
	out := map[string]javaAnnotationSpec{}
	spring := "org.springframework.web.bind.annotation."
	out[spring+"RequestMapping"] = javaAnnotationSpec{"spring", javaRequestMapping, ""}
	out[spring+"RequestParam"] = javaAnnotationSpec{"spring", javaQueryParam, ""}
	out[spring+"PathVariable"] = javaAnnotationSpec{"spring", javaPathParam, ""}
	out[spring+"RequestHeader"] = javaAnnotationSpec{"spring", javaHeaderParam, ""}
	out[spring+"RequestBody"] = javaAnnotationSpec{"spring", javaBodyParam, ""}
	micronaut := "io.micronaut.http.annotation."
	out[micronaut+"Controller"] = javaAnnotationSpec{"micronaut", javaMicronautController, ""}
	out[micronaut+"QueryValue"] = javaAnnotationSpec{"micronaut", javaQueryParam, ""}
	out[micronaut+"PathVariable"] = javaAnnotationSpec{"micronaut", javaPathParam, ""}
	out[micronaut+"Header"] = javaAnnotationSpec{"micronaut", javaHeaderParam, ""}
	out[micronaut+"Body"] = javaAnnotationSpec{"micronaut", javaBodyParam, ""}
	for _, verb := range []string{"Get", "Post", "Put", "Delete", "Patch"} {
		method := strings.ToUpper(verb)
		out[spring+verb+"Mapping"] = javaAnnotationSpec{"spring", javaVerb, method}
		out[micronaut+verb] = javaAnnotationSpec{"micronaut", javaVerb, method}
	}
	for _, jax := range []string{"javax.ws.rs.", "jakarta.ws.rs."} {
		out[jax+"Path"] = javaAnnotationSpec{"jaxrs", javaJaxPath, ""}
		out[jax+"QueryParam"] = javaAnnotationSpec{"jaxrs", javaQueryParam, ""}
		out[jax+"PathParam"] = javaAnnotationSpec{"jaxrs", javaPathParam, ""}
		out[jax+"HeaderParam"] = javaAnnotationSpec{"jaxrs", javaHeaderParam, ""}
		out[jax+"DefaultValue"] = javaAnnotationSpec{"jaxrs", javaDefaultValue, ""}
		for _, verb := range []string{"GET", "POST", "PUT", "DELETE", "PATCH"} {
			out[jax+verb] = javaAnnotationSpec{"jaxrs", javaVerb, verb}
		}
	}
	return out
}()

const springRequestMethod = "org.springframework.web.bind.annotation.RequestMethod"

// Wrapper return types whose single type argument is the serialized body.
var javaResponseWrappers = map[string]bool{
	"ResponseEntity": true, "HttpEntity": true, "Mono": true, "Optional": true,
	"CompletableFuture": true, "CompletionStage": true, "HttpResponse": true,
	"MutableHttpResponse": true, "Single": true, "Maybe": true, "Uni": true,
}

var javaValidationAnnotations = map[string]bool{"Valid": true, "Validated": true, "NotNull": true, "Nonnull": true, "Nullable": true}

type javaImport struct {
	path     string
	wildcard bool
	static   bool
}

type javaTypeRef struct {
	name string
	args []javaTypeRef
}

type javaField struct {
	name string
	typ  javaTypeRef
}

type javaTypeDecl struct {
	fqn    string
	outer  string
	fields []javaField
	source string
}

type javaParam struct {
	kind     javaAnnotationKind
	name     string
	variable string
	optional bool
	typ      javaTypeRef
	explicit bool
	plain    bool
}

type javaRouteDecl struct {
	method   string
	path     string
	source   string
	params   []javaParam
	response javaTypeRef
	outer    string
	auth     bool
}

// javaFacts is everything inference needs from one Java file, extracted so
// the syntax tree can be released immediately.
type javaFacts struct {
	path    string
	pkg     string
	imports []javaImport
	types   []javaTypeDecl
	routes  []javaRouteDecl
	notes   []string
}

func extractJavaFacts(f *syntaxFile) *javaFacts {
	facts := &javaFacts{path: f.path}
	for _, child := range f.named(f.root) {
		switch f.typ(child) {
		case "package_declaration":
			if name := f.firstNamed(child, "scoped_identifier", "identifier"); name != nil {
				facts.pkg = compactJavaName(f.text(name))
			}
		case "import_declaration":
			name := f.firstNamed(child, "scoped_identifier", "identifier")
			if name == nil {
				continue
			}
			facts.imports = append(facts.imports, javaImport{
				path:     compactJavaName(f.text(name)),
				wildcard: f.firstNamed(child, "asterisk") != nil,
				static:   strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(f.text(child), "import")), "static"),
			})
		}
	}
	for _, child := range f.named(f.root) {
		facts.extractType(f, child, "")
	}
	return facts
}

func compactJavaName(name string) string {
	return strings.Join(strings.Fields(name), "")
}

func (j *javaFacts) qualify(outer, simple string) string {
	switch {
	case outer != "":
		return outer + "." + simple
	case j.pkg != "":
		return j.pkg + "." + simple
	default:
		return simple
	}
}

func (j *javaFacts) extractType(f *syntaxFile, n *ts.Node, outer string) {
	typ := f.typ(n)
	switch typ {
	case "class_declaration", "interface_declaration", "record_declaration", "enum_declaration":
	default:
		return
	}
	name := f.text(f.field(n, "name"))
	if name == "" {
		return
	}
	fqn := j.qualify(outer, name)
	decl := javaTypeDecl{fqn: fqn, outer: outer, source: f.source(n)}
	body := f.field(n, "body")
	if typ == "record_declaration" {
		for _, param := range f.namedOfType(f.field(n, "parameters"), "formal_parameter") {
			if field, ok := j.javaFieldFrom(f, param, f.field(param, "name")); ok {
				decl.fields = append(decl.fields, field)
			}
		}
	}
	for _, member := range f.named(body) {
		if f.typ(member) != "field_declaration" || javaHasModifier(f, member, "static") {
			continue
		}
		for _, declarator := range f.namedOfType(member, "variable_declarator") {
			if field, ok := j.javaFieldFrom(f, member, f.field(declarator, "name")); ok {
				decl.fields = append(decl.fields, field)
			}
		}
	}
	j.types = append(j.types, decl)
	if typ == "class_declaration" || typ == "interface_declaration" {
		j.extractRoutes(f, n, fqn)
	}
	for _, member := range f.named(body) {
		j.extractType(f, member, fqn)
	}
}

// javaFieldFrom reads a serialized field name, honoring @JsonProperty and
// skipping @JsonIgnore.
func (j *javaFacts) javaFieldFrom(f *syntaxFile, owner, nameNode *ts.Node) (javaField, bool) {
	name := f.text(nameNode)
	for _, annotation := range javaAnnotationNodes(f, owner) {
		switch javaSimpleName(f, annotation) {
		case "JsonIgnore":
			return javaField{}, false
		case "JsonProperty":
			args, ok := javaAnnotationArgs(f, annotation)
			if !ok {
				return javaField{}, false
			}
			if value := args["value"]; value != nil {
				renamed, ok := f.staticString(value)
				if !ok {
					return javaField{}, false
				}
				name = renamed
			}
		}
	}
	if name == "" {
		return javaField{}, false
	}
	return javaField{name: name, typ: javaTypeOf(f, f.field(owner, "type"))}, true
}

func javaTypeOf(f *syntaxFile, n *ts.Node) javaTypeRef {
	switch f.typ(n) {
	case "type_identifier", "scoped_type_identifier", "integral_type", "floating_point_type", "boolean_type", "void_type":
		return javaTypeRef{name: compactJavaName(f.text(n))}
	case "generic_type":
		ref := javaTypeRef{}
		for _, child := range f.named(n) {
			switch f.typ(child) {
			case "type_identifier", "scoped_type_identifier":
				ref.name = compactJavaName(f.text(child))
			case "type_arguments":
				for _, arg := range f.named(child) {
					ref.args = append(ref.args, javaTypeOf(f, arg))
				}
			}
		}
		return ref
	case "annotated_type":
		children := f.named(n)
		if len(children) > 0 {
			return javaTypeOf(f, children[len(children)-1])
		}
	case "array_type":
		return javaTypeRef{name: "[]"}
	}
	return javaTypeRef{}
}

func (r javaTypeRef) simple() string {
	if dot := strings.LastIndex(r.name, "."); dot >= 0 {
		return r.name[dot+1:]
	}
	return r.name
}

func (r javaTypeRef) isString() bool {
	return r.simple() == "String" || r.simple() == "CharSequence"
}

func javaHasModifier(f *syntaxFile, n *ts.Node, keyword string) bool {
	modifiers := f.firstNamed(n, "modifiers")
	if modifiers == nil {
		return false
	}
	for i := 0; i < modifiers.ChildCount(); i++ {
		if child := modifiers.Child(i); child != nil && f.typ(child) == keyword {
			return true
		}
	}
	return false
}

func javaAnnotationNodes(f *syntaxFile, n *ts.Node) []*ts.Node {
	return f.namedOfType(f.firstNamed(n, "modifiers"), "annotation", "marker_annotation")
}

func javaSimpleName(f *syntaxFile, annotation *ts.Node) string {
	name := compactJavaName(f.text(f.field(annotation, "name")))
	if dot := strings.LastIndex(name, "."); dot >= 0 {
		return name[dot+1:]
	}
	return name
}

// resolve maps a simple or qualified name to the single fully qualified name
// the file's imports make visible among known names.
func (j *javaFacts) resolve(name string, known func(string) bool) string {
	if strings.Contains(name, ".") {
		if known(name) {
			return name
		}
		return ""
	}
	for _, imp := range j.imports {
		if !imp.wildcard && !imp.static && strings.HasSuffix(imp.path, "."+name) {
			if known(imp.path) {
				return imp.path
			}
			return ""
		}
	}
	var matches []string
	for _, imp := range j.imports {
		if imp.wildcard && !imp.static && known(imp.path+"."+name) {
			matches = append(matches, imp.path+"."+name)
		}
	}
	if len(uniqueStrings(matches)) == 1 {
		return matches[0]
	}
	return ""
}

type javaResolvedAnnotation struct {
	node *ts.Node
	spec javaAnnotationSpec
}

func (j *javaFacts) frameworkAnnotations(f *syntaxFile, n *ts.Node) []javaResolvedAnnotation {
	var out []javaResolvedAnnotation
	for _, annotation := range javaAnnotationNodes(f, n) {
		fqn := j.resolve(compactJavaName(f.text(f.field(annotation, "name"))), func(name string) bool {
			_, ok := javaAnnotations[name]
			return ok
		})
		if fqn != "" {
			out = append(out, javaResolvedAnnotation{node: annotation, spec: javaAnnotations[fqn]})
		}
	}
	return out
}

// javaAnnotationArgs returns annotation elements by name; a lone element is
// "value". ok is false when the argument list has an unexpected shape.
func javaAnnotationArgs(f *syntaxFile, annotation *ts.Node) (map[string]*ts.Node, bool) {
	out := map[string]*ts.Node{}
	if f.typ(annotation) == "marker_annotation" {
		return out, true
	}
	for _, arg := range f.named(f.field(annotation, "arguments")) {
		if f.typ(arg) == "element_value_pair" {
			out[f.text(f.field(arg, "key"))] = f.field(arg, "value")
			continue
		}
		if _, exists := out["value"]; exists {
			return nil, false
		}
		out["value"] = arg
	}
	return out, true
}

// javaStaticStrings evaluates a single constant or an array initializer of
// constants.
func javaStaticStrings(f *syntaxFile, n *ts.Node) ([]string, bool) {
	if f.typ(n) == "element_value_array_initializer" {
		var out []string
		for _, element := range f.named(n) {
			value, ok := f.staticString(element)
			if !ok {
				return nil, false
			}
			out = append(out, value)
		}
		return out, len(out) > 0
	}
	value, ok := f.staticString(n)
	if !ok {
		return nil, false
	}
	return []string{value}, true
}

// javaAliasedPaths reads alias elements such as Spring's value/path. When
// more than one alias is set they must agree.
func javaAliasedPaths(f *syntaxFile, args map[string]*ts.Node, aliases ...string) ([]string, bool, bool) {
	var found []string
	present := false
	for _, alias := range aliases {
		node := args[alias]
		if node == nil {
			continue
		}
		values, ok := javaStaticStrings(f, node)
		if !ok {
			return nil, true, false
		}
		if present && strings.Join(values, "\x00") != strings.Join(found, "\x00") {
			return nil, true, false
		}
		found, present = values, true
	}
	return found, present, true
}

func (j *javaFacts) springMethods(f *syntaxFile, n *ts.Node) ([]string, bool) {
	elements := []*ts.Node{n}
	if f.typ(n) == "element_value_array_initializer" {
		elements = f.named(n)
	}
	var methods []string
	for _, element := range elements {
		var constant string
		switch f.typ(element) {
		case "field_access":
			owner := compactJavaName(f.text(f.field(element, "object")))
			if j.resolve(owner, func(name string) bool { return name == springRequestMethod }) == "" {
				return nil, false
			}
			constant = f.text(f.field(element, "field"))
		case "identifier":
			constant = f.text(element)
			proven := false
			for _, imp := range j.imports {
				if imp.static && (imp.path == springRequestMethod+"."+constant || (imp.wildcard && imp.path == springRequestMethod)) {
					proven = true
				}
			}
			if !proven {
				return nil, false
			}
		default:
			return nil, false
		}
		switch constant {
		case "GET", "POST", "PUT", "DELETE", "PATCH":
			methods = append(methods, constant)
		default:
			return nil, false
		}
	}
	return uniqueStrings(methods), len(methods) > 0
}

type javaClassRoutes struct {
	springPaths   []string
	springMethods []string
	springOK      bool
	jaxPath       string
	jaxOK         bool
	jaxPresent    bool
	mnPath        string
	mnOK          bool
	mnPresent     bool
	auth          bool
}

func (j *javaFacts) classRoutes(f *syntaxFile, n *ts.Node) javaClassRoutes {
	out := javaClassRoutes{springPaths: []string{""}, springOK: true, jaxOK: true, mnOK: true}
	for _, annotation := range javaAnnotationNodes(f, n) {
		if javaSimpleName(f, annotation) == "HarnessAuth" {
			out.auth = true
		}
	}
	for _, resolved := range j.frameworkAnnotations(f, n) {
		args, ok := javaAnnotationArgs(f, resolved.node)
		switch resolved.spec.kind {
		case javaRequestMapping:
			paths, present, valid := javaAliasedPaths(f, args, "value", "path")
			if !ok || !valid {
				out.springOK = false
				continue
			}
			if present {
				out.springPaths = paths
			}
			if methods := args["method"]; methods != nil {
				out.springMethods, out.springOK = j.springMethods(f, methods)
			}
		case javaJaxPath:
			paths, present, valid := javaAliasedPaths(f, args, "value")
			out.jaxPresent = true
			out.jaxOK = ok && valid && present && len(paths) == 1
			if out.jaxOK {
				out.jaxPath = paths[0]
			}
		case javaMicronautController:
			paths, present, valid := javaAliasedPaths(f, args, "value")
			out.mnPresent = true
			out.mnOK = ok && valid && (!present || len(paths) == 1)
			out.mnPath = "/"
			if out.mnOK && present {
				out.mnPath = paths[0]
			}
		}
	}
	return out
}

func (j *javaFacts) note(f *syntaxFile, n *ts.Node, format string, args ...any) {
	j.notes = append(j.notes, fmt.Sprintf("Skipped unsupported route at %s: %s", f.source(n), fmt.Sprintf(format, args...)))
}

func (j *javaFacts) extractRoutes(f *syntaxFile, class *ts.Node, fqn string) {
	class0 := j.classRoutes(f, class)
	for _, method := range f.namedOfType(f.field(class, "body"), "method_declaration") {
		annotations := j.frameworkAnnotations(f, method)
		frameworks := map[string]bool{}
		var routeAnnotations []javaResolvedAnnotation
		for _, resolved := range annotations {
			switch resolved.spec.kind {
			case javaRequestMapping, javaVerb, javaJaxPath:
				frameworks[resolved.spec.framework] = true
				routeAnnotations = append(routeAnnotations, resolved)
			}
		}
		if len(frameworks) == 0 {
			continue
		}
		if len(frameworks) > 1 {
			j.note(f, method, "route annotations from more than one framework")
			continue
		}
		var framework string
		for name := range frameworks {
			framework = name
		}
		methods, paths, anchor, ok := j.methodRoute(f, method, framework, class0, routeAnnotations)
		if !ok {
			continue
		}
		params := j.javaParams(f, method, framework)
		auth := class0.auth
		for _, annotation := range javaAnnotationNodes(f, method) {
			if javaSimpleName(f, annotation) == "HarnessAuth" {
				auth = true
			}
		}
		for _, httpMethod := range methods {
			for _, routePath := range paths {
				j.routes = append(j.routes, javaRouteDecl{
					method:   httpMethod,
					path:     routePath,
					source:   f.source(anchor),
					params:   params,
					response: javaTypeOf(f, f.field(method, "type")),
					outer:    fqn,
					auth:     auth,
				})
			}
		}
	}
}

func (j *javaFacts) methodRoute(f *syntaxFile, method *ts.Node, framework string, class javaClassRoutes, annotations []javaResolvedAnnotation) ([]string, []string, *ts.Node, bool) {
	switch framework {
	case "spring":
		if !class.springOK {
			j.note(f, method, "the class-level @RequestMapping is not a literal mapping")
			return nil, nil, nil, false
		}
		if len(annotations) != 1 {
			j.note(f, method, "more than one Spring mapping annotation")
			return nil, nil, nil, false
		}
		anchor := annotations[0]
		args, ok := javaAnnotationArgs(f, anchor.node)
		paths, present, valid := javaAliasedPaths(f, args, "value", "path")
		if !ok || !valid {
			j.note(f, method, "%s path is not a single literal mapping", f.text(anchor.node))
			return nil, nil, nil, false
		}
		if !present {
			paths = []string{""}
		}
		methods := []string{anchor.spec.method}
		if anchor.spec.kind == javaRequestMapping {
			methods = class.springMethods
			if node := args["method"]; node != nil {
				methods, ok = j.springMethods(f, node)
				if !ok {
					j.note(f, method, "%s method is not a supported RequestMethod constant", f.text(anchor.node))
					return nil, nil, nil, false
				}
			}
			if len(methods) == 0 {
				j.note(f, method, "%s does not declare an HTTP method", f.text(anchor.node))
				return nil, nil, nil, false
			}
		}
		var full []string
		for _, prefix := range class.springPaths {
			for _, suffix := range paths {
				full = append(full, joinURLPath(prefix, suffix))
			}
		}
		return methods, full, anchor.node, true
	case "jaxrs":
		var verbs []javaResolvedAnnotation
		methodPath := ""
		for _, annotation := range annotations {
			if annotation.spec.kind == javaVerb {
				verbs = append(verbs, annotation)
				continue
			}
			args, ok := javaAnnotationArgs(f, annotation.node)
			paths, present, valid := javaAliasedPaths(f, args, "value")
			if !ok || !valid || !present || len(paths) != 1 {
				j.note(f, method, "@Path is not a single literal")
				return nil, nil, nil, false
			}
			methodPath = paths[0]
		}
		if len(verbs) != 1 {
			j.note(f, method, "JAX-RS resource method needs exactly one HTTP method annotation")
			return nil, nil, nil, false
		}
		if !class.jaxPresent || !class.jaxOK {
			j.note(f, method, "the enclosing class has no literal root @Path")
			return nil, nil, nil, false
		}
		return []string{verbs[0].spec.method}, []string{joinURLPath(class.jaxPath, methodPath)}, verbs[0].node, true
	default:
		if len(annotations) != 1 {
			j.note(f, method, "more than one Micronaut route annotation")
			return nil, nil, nil, false
		}
		if !class.mnPresent || !class.mnOK {
			j.note(f, method, "the enclosing class has no literal @Controller")
			return nil, nil, nil, false
		}
		anchor := annotations[0]
		args, ok := javaAnnotationArgs(f, anchor.node)
		paths, present, valid := javaAliasedPaths(f, args, "value", "uri")
		if !ok || !valid {
			j.note(f, method, "%s path is not a literal", f.text(anchor.node))
			return nil, nil, nil, false
		}
		if extra := args["uris"]; extra != nil {
			more, valid := javaStaticStrings(f, extra)
			if !valid {
				j.note(f, method, "%s uris are not literals", f.text(anchor.node))
				return nil, nil, nil, false
			}
			paths, present = append(paths, more...), true
		}
		if !present {
			paths = []string{"/"}
		}
		var full []string
		for _, suffix := range uniqueStrings(paths) {
			full = append(full, joinURLPath(class.mnPath, suffix))
		}
		return []string{anchor.spec.method}, full, anchor.node, true
	}
}

func (j *javaFacts) javaParams(f *syntaxFile, method *ts.Node, framework string) []javaParam {
	var params []javaParam
	for _, param := range f.namedOfType(f.field(method, "parameters"), "formal_parameter") {
		variable := f.text(f.field(param, "name"))
		out := javaParam{variable: variable, name: variable, typ: javaTypeOf(f, f.field(param, "type")), plain: true}
		hasDefault := false
		for _, annotation := range javaAnnotationNodes(f, param) {
			if !javaValidationAnnotations[javaSimpleName(f, annotation)] {
				out.plain = false
			}
		}
		for _, resolved := range j.frameworkAnnotations(f, param) {
			if resolved.spec.framework != framework {
				continue
			}
			if resolved.spec.kind == javaDefaultValue {
				hasDefault = true
				continue
			}
			args, ok := javaAnnotationArgs(f, resolved.node)
			if !ok {
				continue
			}
			out.kind = resolved.spec.kind
			for _, key := range []string{"value", "name"} {
				if node := args[key]; node != nil {
					if name, ok := f.staticString(node); ok && name != "" {
						out.name, out.explicit = name, true
					}
				}
			}
			if required := args["required"]; required != nil && f.text(required) == "false" {
				out.optional = true
			}
			if args["defaultValue"] != nil {
				out.optional = true
			}
		}
		if hasDefault || out.typ.simple() == "Optional" {
			out.optional = true
		}
		params = append(params, out)
	}
	return params
}

// javaService resolves types across every parsed Java file in one service.
type javaService struct {
	types map[string]javaTypeDecl
	owner map[string]*javaFacts
}

func newJavaService(files []*javaFacts) *javaService {
	svc := &javaService{types: map[string]javaTypeDecl{}, owner: map[string]*javaFacts{}}
	for _, file := range files {
		for _, decl := range file.types {
			if _, dup := svc.types[decl.fqn]; dup {
				svc.types[decl.fqn] = javaTypeDecl{fqn: decl.fqn}
				continue
			}
			svc.types[decl.fqn] = decl
			svc.owner[decl.fqn] = file
		}
	}
	return svc
}

// resolveType follows Java scoping: enclosing types, single-type imports, the
// current package, then unambiguous wildcard imports.
func (s *javaService) resolveType(file *javaFacts, outer string, ref javaTypeRef) (javaTypeDecl, bool) {
	name := ref.name
	if name == "" {
		return javaTypeDecl{}, false
	}
	first, rest, _ := strings.Cut(name, ".")
	suffix := ""
	if rest != "" {
		suffix = "." + rest
	}
	lookup := func(fqn string) (javaTypeDecl, bool) {
		decl, ok := s.types[fqn+suffix]
		return decl, ok && decl.source != ""
	}
	for scope := outer; scope != ""; {
		if decl, ok := lookup(scope + "." + first); ok {
			return decl, true
		}
		parent := s.types[scope].outer
		if parent == "" {
			break
		}
		scope = parent
	}
	for _, imp := range file.imports {
		if !imp.wildcard && !imp.static && strings.HasSuffix(imp.path, "."+first) {
			return lookup(imp.path)
		}
	}
	if decl, ok := lookup(file.qualify("", first)); ok {
		return decl, true
	}
	var found []javaTypeDecl
	for _, imp := range file.imports {
		if imp.wildcard && !imp.static {
			if decl, ok := lookup(imp.path + "." + first); ok {
				found = append(found, decl)
			}
		}
	}
	if len(found) == 1 {
		return found[0], true
	}
	if rest != "" {
		return lookup(name)
	}
	return javaTypeDecl{}, false
}

func (s *javaService) dtoShape(file *javaFacts, outer string, ref javaTypeRef, depth int) *dtoShape {
	decl, ok := s.resolveType(file, outer, ref)
	if !ok || depth > 3 {
		return nil
	}
	owner := s.owner[decl.fqn]
	shape := &dtoShape{source: decl.source}
	for _, field := range decl.fields {
		item := dtoShapeField{name: field.name, isString: field.typ.isString()}
		if !item.isString {
			item.nested = s.dtoShape(owner, decl.fqn, field.typ, depth+1)
		}
		shape.fields = append(shape.fields, item)
	}
	return shape
}

func unwrapJavaResponse(ref javaTypeRef) javaTypeRef {
	for javaResponseWrappers[ref.simple()] && len(ref.args) == 1 {
		ref = ref.args[0]
	}
	return ref
}

func javaServiceCandidates(root string, files []*javaFacts) []RouteCandidate {
	svc := newJavaService(files)
	var out []RouteCandidate
	for _, file := range files {
		for _, route := range file.routes {
			candidate := newAdapterRoute("java", root, route.method, route.path, route.source)
			if root == "." {
				candidate.Agent = slugIdentifier(packageAgent(file.pkg))
			}
			candidate.AuthRequired = route.auth
			var explicitBody, plainBody []javaParam
			var headers []javaParam
			for _, param := range route.params {
				switch param.kind {
				case javaQueryParam:
					if !param.optional {
						candidate.QueryParams = append(candidate.QueryParams, param.name)
						candidate.QueryMappings = append(candidate.QueryMappings, param.name+":"+param.variable)
					}
				case javaPathParam:
					candidate.PathParams = append(candidate.PathParams, param.name)
					candidate.PathMappings = append(candidate.PathMappings, param.name+":"+param.variable)
				case javaHeaderParam:
					headers = append(headers, param)
				case javaBodyParam:
					explicitBody = append(explicitBody, param)
				default:
					if param.plain {
						if _, ok := svc.resolveType(file, route.outer, param.typ); ok {
							plainBody = append(plainBody, param)
						}
					}
				}
			}
			candidate.Header = chooseRouteHeader(headerNames(headers))
			body := explicitBody
			if len(body) == 0 {
				body = plainBody
			}
			if len(body) == 1 {
				candidate.request = svc.dtoShape(file, route.outer, body[0].typ, 0)
			}
			candidate.response = svc.dtoShape(file, route.outer, unwrapJavaResponse(route.response), 0)
			out = append(out, candidate)
		}
	}
	return out
}

func headerNames(params []javaParam) []string {
	var out []string
	for _, param := range params {
		if param.name != "" {
			out = append(out, param.name)
		}
	}
	return out
}

// chooseRouteHeader keeps a declared auth header, or a lone declared header.
func chooseRouteHeader(names []string) string {
	names = uniqueStrings(names)
	for _, name := range names {
		switch canonical := canonicalHeader(name); canonical {
		case "Authorization", "x-api-key":
			return canonical
		}
	}
	if len(names) == 1 {
		return canonicalHeader(names[0])
	}
	return ""
}

// extractJavaTestCalls reads MockMvc request chains. The request builder and
// perform receiver must resolve to Spring MockMvc bindings, so an unrelated
// fluent post(...)/perform(...) chain cannot become staging evidence.
func extractJavaTestCalls(f *syntaxFile) []testCallFact {
	clients, staticBuilders, qualifiedBuilders := javaTestBindings(f)
	var calls []testCallFact
	f.walk(f.root, func(n *ts.Node) bool {
		if f.typ(n) != "method_invocation" {
			return true
		}
		method := strings.ToUpper(f.text(f.field(n, "name")))
		if !isRouteVerb(method) {
			return true
		}
		object := f.field(n, "object")
		if object == nil && !staticBuilders {
			return true
		}
		if object != nil && (!qualifiedBuilders || compactJavaName(f.text(object)) != "MockMvcRequestBuilders") {
			return true
		}
		args := f.named(f.field(n, "arguments"))
		if len(args) == 0 {
			return true
		}
		routePath, ok := f.staticString(args[0])
		if !ok {
			return true
		}
		call := testCallFact{route: canonicalRouteIdentity(method, routePath), source: f.source(n)}
		current := n
		for {
			parent := chainedCall(f, current, "method_invocation", "object")
			if parent == nil {
				break
			}
			current = parent
			chained := f.named(f.field(parent, "arguments"))
			switch f.text(f.field(parent, "name")) {
			case "header":
				if len(chained) == 2 {
					call.headers = append(call.headers, javaTestHeader(f, parent, chained[0], chained[1]))
				}
			case "content":
				if len(chained) == 1 {
					if raw, ok := javaLocalString(f, parent, chained[0]); ok {
						call.body, call.bodyStatic = jsonObjectStrings(raw)
					}
				}
			}
		}
		perform := enclosingCallOfArgument(f, current, "method_invocation")
		if perform == nil || f.text(f.field(perform, "name")) != "perform" {
			return true
		}
		if !javaMockMvcReceiver(f, f.field(perform, "object"), clients) {
			return true
		}
		current = perform
		for {
			parent := chainedCall(f, current, "method_invocation", "object")
			if parent == nil {
				break
			}
			current = parent
			if f.text(f.field(parent, "name")) == "andExpect" {
				for _, arg := range f.named(f.field(parent, "arguments")) {
					if assertion, ok := javaJSONPathAssertion(f, arg); ok {
						call.assertions = append(call.assertions, assertion)
					}
				}
			}
		}
		calls = append(calls, call)
		return true
	})
	return calls
}

func javaTestBindings(f *syntaxFile) (clients map[string]bool, staticBuilders, qualifiedBuilders bool) {
	clients = map[string]bool{}
	hasMockMVC := false
	for _, statement := range f.named(f.root) {
		if f.typ(statement) != "import_declaration" {
			continue
		}
		text := compactJavaName(f.text(statement))
		switch {
		case strings.Contains(text, "org.springframework.test.web.servlet.MockMvc;"):
			hasMockMVC = true
		case strings.Contains(text, "org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;"):
			staticBuilders = true
		case strings.Contains(text, "org.springframework.test.web.servlet.request.MockMvcRequestBuilders;"):
			qualifiedBuilders = true
		}
	}
	if !hasMockMVC {
		return clients, staticBuilders, qualifiedBuilders
	}
	f.walk(f.root, func(n *ts.Node) bool {
		switch f.typ(n) {
		case "field_declaration", "local_variable_declaration":
			if compactJavaName(f.text(f.field(n, "type"))) != "MockMvc" {
				return true
			}
			for _, declarator := range f.namedOfType(n, "variable_declarator") {
				clients[f.text(f.field(declarator, "name"))] = true
			}
		case "formal_parameter":
			if compactJavaName(f.text(f.field(n, "type"))) == "MockMvc" {
				clients[f.text(f.field(n, "name"))] = true
			}
		}
		return true
	})
	return clients, staticBuilders, qualifiedBuilders
}

func javaMockMvcReceiver(f *syntaxFile, receiver *ts.Node, clients map[string]bool) bool {
	switch f.typ(receiver) {
	case "identifier":
		return clients[f.text(receiver)]
	case "field_access":
		return clients[f.text(f.field(receiver, "field"))]
	default:
		return false
	}
}

func javaTestHeader(f *syntaxFile, call, name, value *ts.Node) testHeader {
	header := testHeader{source: f.source(call)}
	if literal, ok := f.staticString(name); ok {
		header.name = literal
	} else if f.typ(name) == "field_access" && strings.HasSuffix(compactJavaName(f.text(name)), "HttpHeaders.AUTHORIZATION") {
		header.name = "Authorization"
	}
	header.env = javaEnvName(f, call, value)
	return header
}

func javaEnvName(f *syntaxFile, at, value *ts.Node) string {
	value = f.unwrapParens(value)
	switch f.typ(value) {
	case "method_invocation":
		args := f.named(f.field(value, "arguments"))
		if f.text(f.field(value, "object")) == "System" && f.text(f.field(value, "name")) == "getenv" && len(args) == 1 {
			name, _ := f.staticString(args[0])
			return name
		}
	case "identifier":
		if init := javaLocalInitializer(f, at, f.text(value)); init != nil {
			return javaEnvName(f, init, init)
		}
	}
	return ""
}

func javaLocalString(f *syntaxFile, at, value *ts.Node) (string, bool) {
	if f.typ(value) == "identifier" {
		if init := javaLocalInitializer(f, at, f.text(value)); init != nil {
			return f.staticString(init)
		}
		return "", false
	}
	return f.staticString(value)
}

// javaLocalInitializer finds the nearest preceding local declaration of name
// in the blocks enclosing at.
func javaLocalInitializer(f *syntaxFile, at *ts.Node, name string) *ts.Node {
	for scope := at.Parent(); scope != nil; scope = scope.Parent() {
		if f.typ(scope) != "block" {
			continue
		}
		var found *ts.Node
		for _, statement := range f.namedOfType(scope, "local_variable_declaration") {
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
			return found
		}
	}
	return nil
}

func javaJSONPathAssertion(f *syntaxFile, n *ts.Node) (testAssertion, bool) {
	if f.typ(n) != "method_invocation" || f.text(f.field(n, "name")) != "value" {
		return testAssertion{}, false
	}
	matcher := f.field(n, "object")
	if f.typ(matcher) != "method_invocation" || f.text(f.field(matcher, "name")) != "jsonPath" {
		return testAssertion{}, false
	}
	pathArgs, valueArgs := f.named(f.field(matcher, "arguments")), f.named(f.field(n, "arguments"))
	if len(pathArgs) != 1 || len(valueArgs) != 1 {
		return testAssertion{}, false
	}
	rawPath, ok := f.staticString(pathArgs[0])
	if !ok || !strings.HasPrefix(rawPath, "$.") {
		return testAssertion{}, false
	}
	value, ok := f.staticString(valueArgs[0])
	if !ok || !validResponsePath(rawPath[2:]) {
		return testAssertion{}, false
	}
	return testAssertion{path: rawPath[2:], value: value, source: f.source(n)}, true
}
