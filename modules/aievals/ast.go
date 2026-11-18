// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	ts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

// Adapter inference reads syntax trees only. Comments, string bodies, and
// unparsable files never reach route, DTO, port, or test-call extraction,
// because those facts are read from typed nodes rather than raw text.

type sourceKind int

const (
	sourceNone sourceKind = iota
	sourceJava
	sourcePython
	sourceJavaScript
	sourceTypeScript
	sourceTSX
)

func sourceKindFor(path string) sourceKind {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".java":
		return sourceJava
	case ".py":
		return sourcePython
	case ".js", ".mjs", ".cjs":
		return sourceJavaScript
	case ".ts", ".mts", ".cts":
		return sourceTypeScript
	case ".tsx":
		return sourceTSX
	default:
		return sourceNone
	}
}

func (k sourceKind) language() *ts.Language {
	switch k {
	case sourceJava:
		return grammars.JavaLanguage()
	case sourcePython:
		return grammars.PythonLanguage()
	case sourceJavaScript:
		return grammars.JavascriptLanguage()
	case sourceTypeScript:
		return grammars.TypescriptLanguage()
	case sourceTSX:
		return grammars.TsxLanguage()
	default:
		return nil
	}
}

var errSyntax = errors.New("source has syntax errors")

// syntaxFile pairs a parsed tree with the bytes and grammar it was built from.
// Nodes are only meaningful through these helpers.
type syntaxFile struct {
	path string
	src  []byte
	kind sourceKind
	lang *ts.Language
	tree *ts.Tree
	root *ts.Node
}

// parserSet keeps one parser per grammar for a single inference run; parsers
// are not shared across goroutines.
type parserSet map[sourceKind]*ts.Parser

func (p parserSet) parse(path, body string) (*syntaxFile, error) {
	kind := sourceKindFor(path)
	lang := kind.language()
	if lang == nil {
		return nil, fmt.Errorf("%s: unsupported source language", path)
	}
	parser := p[kind]
	if parser == nil {
		parser = ts.NewParser(lang)
		p[kind] = parser
	}
	src := []byte(body)
	tree, err := parser.Parse(src)
	if err != nil {
		return nil, err
	}
	root := tree.RootNode()
	if root == nil || tree.ParseStoppedEarly() || root.HasErrorOrMissing() {
		tree.Release()
		return nil, errSyntax
	}
	return &syntaxFile{path: path, src: src, kind: kind, lang: lang, tree: tree, root: root}, nil
}

func (f *syntaxFile) release() {
	if f != nil && f.tree != nil {
		f.tree.Release()
		f.tree = nil
	}
}

func (f *syntaxFile) typ(n *ts.Node) string {
	if n == nil {
		return ""
	}
	return n.Type(f.lang)
}

func (f *syntaxFile) text(n *ts.Node) string {
	if n == nil {
		return ""
	}
	return n.Text(f.src)
}

func (f *syntaxFile) field(n *ts.Node, name string) *ts.Node {
	if n == nil {
		return nil
	}
	return n.ChildByFieldName(name, f.lang)
}

// named returns named, non-comment children.
func (f *syntaxFile) named(n *ts.Node) []*ts.Node {
	if n == nil {
		return nil
	}
	count := n.NamedChildCount()
	out := make([]*ts.Node, 0, count)
	for i := 0; i < count; i++ {
		child := n.NamedChild(i)
		if child == nil || child.IsExtra() || strings.Contains(f.typ(child), "comment") {
			continue
		}
		out = append(out, child)
	}
	return out
}

func (f *syntaxFile) namedOfType(n *ts.Node, types ...string) []*ts.Node {
	var out []*ts.Node
	for _, child := range f.named(n) {
		for _, typ := range types {
			if f.typ(child) == typ {
				out = append(out, child)
				break
			}
		}
	}
	return out
}

func (f *syntaxFile) firstNamed(n *ts.Node, types ...string) *ts.Node {
	if found := f.namedOfType(n, types...); len(found) > 0 {
		return found[0]
	}
	return nil
}

// walk visits n and its non-comment descendants in source order. Returning
// false from visit skips the node's children.
func (f *syntaxFile) walk(n *ts.Node, visit func(*ts.Node) bool) {
	if n == nil || !visit(n) {
		return
	}
	for _, child := range f.named(n) {
		f.walk(child, visit)
	}
}

func (f *syntaxFile) line(n *ts.Node) int {
	if n == nil {
		return 1
	}
	return int(n.StartPoint().Row) + 1
}

func (f *syntaxFile) source(n *ts.Node) string {
	return fmt.Sprintf("%s:%d", f.path, f.line(n))
}

// operator returns the anonymous operator token between a binary node's
// operands.
func (f *syntaxFile) operator(n *ts.Node) string {
	if op := f.field(n, "operator"); op != nil {
		return strings.TrimSpace(f.text(op))
	}
	left, right := f.field(n, "left"), f.field(n, "right")
	if left == nil || right == nil {
		return ""
	}
	return strings.TrimSpace(string(f.src[left.EndByte():right.StartByte()]))
}

func (f *syntaxFile) unwrapParens(n *ts.Node) *ts.Node {
	for n != nil && f.typ(n) == "parenthesized_expression" {
		inner := f.named(n)
		if len(inner) != 1 {
			return n
		}
		n = inner[0]
	}
	return n
}

// staticString evaluates n as a compile-time string constant. Anything that
// depends on runtime values (identifiers, interpolations, calls) is refused.
func (f *syntaxFile) staticString(n *ts.Node) (string, bool) {
	n = f.unwrapParens(n)
	if n == nil {
		return "", false
	}
	switch f.kind {
	case sourceJava:
		return f.javaStaticString(n)
	case sourcePython:
		return f.pythonStaticString(n)
	default:
		return f.jsStaticString(n)
	}
}

func (f *syntaxFile) concatStatic(n *ts.Node) (string, bool) {
	if f.operator(n) != "+" {
		return "", false
	}
	left, ok := f.staticString(f.field(n, "left"))
	if !ok {
		return "", false
	}
	right, ok := f.staticString(f.field(n, "right"))
	if !ok {
		return "", false
	}
	return left + right, true
}

func (f *syntaxFile) javaStaticString(n *ts.Node) (string, bool) {
	switch f.typ(n) {
	case "string_literal":
		raw := f.text(n)
		if strings.HasPrefix(raw, `"""`) && strings.HasSuffix(raw, `"""`) && len(raw) >= 6 {
			return decodeJavaTextBlock(raw[3 : len(raw)-3])
		}
		if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
			return "", false
		}
		return decodeJavaEscapes(raw[1 : len(raw)-1])
	case "binary_expression":
		return f.concatStatic(n)
	default:
		return "", false
	}
}

func (f *syntaxFile) pythonStaticString(n *ts.Node) (string, bool) {
	switch f.typ(n) {
	case "string":
		if len(f.namedOfType(n, "interpolation")) > 0 {
			return "", false
		}
		return decodePythonString(f.text(n))
	case "concatenated_string":
		var b strings.Builder
		for _, part := range f.named(n) {
			value, ok := f.pythonStaticString(part)
			if !ok {
				return "", false
			}
			b.WriteString(value)
		}
		return b.String(), true
	default:
		return "", false
	}
}

func (f *syntaxFile) jsStaticString(n *ts.Node) (string, bool) {
	switch f.typ(n) {
	case "string":
		raw := f.text(n)
		if len(raw) < 2 {
			return "", false
		}
		return decodeJSEscapes(raw[1 : len(raw)-1])
	case "template_string":
		if len(f.namedOfType(n, "template_substitution")) > 0 {
			return "", false
		}
		raw := f.text(n)
		if len(raw) < 2 {
			return "", false
		}
		return decodeJSEscapes(raw[1 : len(raw)-1])
	case "binary_expression":
		return f.concatStatic(n)
	default:
		return "", false
	}
}

func (f *syntaxFile) staticInt(n *ts.Node) (int, bool) {
	n = f.unwrapParens(n)
	switch f.typ(n) {
	case "integer", "number", "decimal_integer_literal":
		value, err := strconv.Atoi(strings.ReplaceAll(f.text(n), "_", ""))
		return value, err == nil
	default:
		return 0, false
	}
}

func validPort(port int) bool { return port > 0 && port <= 65535 }

// decodeJavaTextBlock applies JLS 3.10.6: drop the opening line terminator,
// strip incidental indentation and trailing spaces, then interpret escapes.
func decodeJavaTextBlock(content string) (string, bool) {
	newline := strings.IndexByte(content, '\n')
	if newline < 0 || strings.TrimSpace(content[:newline]) != "" {
		return "", false
	}
	lines := strings.Split(strings.ReplaceAll(content[newline+1:], "\r\n", "\n"), "\n")
	indent := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "" && i != len(lines)-1 {
			continue
		}
		width := len(line) - len(strings.TrimLeft(line, " \t\f"))
		if indent < 0 || width < indent {
			indent = width
		}
	}
	for i, line := range lines {
		if len(line) >= indent && indent > 0 {
			line = line[indent:]
		} else if strings.TrimSpace(line) == "" {
			line = ""
		}
		lines[i] = strings.TrimRight(line, " \t\f")
	}
	return decodeJavaEscapes(strings.Join(lines, "\n"))
}

func decodeJavaEscapes(raw string) (string, bool) {
	var b strings.Builder
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			b.WriteByte(raw[i])
			continue
		}
		if i+1 >= len(raw) {
			return "", false
		}
		i++
		switch c := raw[i]; c {
		case 'b':
			b.WriteByte('\b')
		case 't':
			b.WriteByte('\t')
		case 'n':
			b.WriteByte('\n')
		case 'f':
			b.WriteByte('\f')
		case 'r':
			b.WriteByte('\r')
		case 's':
			b.WriteByte(' ')
		case '"', '\'', '\\':
			b.WriteByte(c)
		case '\n':
		case 'u':
			for i+1 < len(raw) && raw[i+1] == 'u' {
				i++
			}
			if i+5 > len(raw) {
				return "", false
			}
			r, err := strconv.ParseUint(raw[i+1:i+5], 16, 32)
			if err != nil {
				return "", false
			}
			b.WriteRune(rune(r))
			i += 4
		default:
			if c < '0' || c > '7' {
				return "", false
			}
			end := i + 1
			limit := i + 2
			if c <= '3' {
				limit = i + 3
			}
			for end < len(raw) && end < limit && raw[end] >= '0' && raw[end] <= '7' {
				end++
			}
			value, _ := strconv.ParseUint(raw[i:end], 8, 8)
			b.WriteByte(byte(value))
			i = end - 1
		}
	}
	return b.String(), true
}

// decodePythonString decodes one Python string token, honoring its prefix.
// Byte strings are not text and are refused.
func decodePythonString(raw string) (string, bool) {
	prefixEnd := strings.IndexAny(raw, `'"`)
	if prefixEnd < 0 {
		return "", false
	}
	prefix := strings.ToLower(raw[:prefixEnd])
	if strings.Contains(prefix, "b") {
		return "", false
	}
	body := raw[prefixEnd:]
	quote := body[:1]
	if strings.HasPrefix(body, strings.Repeat(quote, 3)) && len(body) >= 6 {
		quote = strings.Repeat(quote, 3)
	}
	if len(body) < 2*len(quote) || !strings.HasSuffix(body, quote) {
		return "", false
	}
	body = body[len(quote) : len(body)-len(quote)]
	if strings.Contains(prefix, "f") {
		body = strings.ReplaceAll(strings.ReplaceAll(body, "{{", "{"), "}}", "}")
	}
	if strings.Contains(prefix, "r") {
		return body, true
	}
	return decodePythonEscapes(body)
}

func decodePythonEscapes(raw string) (string, bool) {
	var b strings.Builder
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' || i+1 >= len(raw) {
			b.WriteByte(raw[i])
			continue
		}
		i++
		switch c := raw[i]; c {
		case '\n':
		case '\\', '\'', '"':
			b.WriteByte(c)
		case 'a':
			b.WriteByte('\a')
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'v':
			b.WriteByte('\v')
		case 'x', 'u', 'U':
			width := map[byte]int{'x': 2, 'u': 4, 'U': 8}[c]
			if i+1+width > len(raw) {
				return "", false
			}
			r, err := strconv.ParseUint(raw[i+1:i+1+width], 16, 32)
			if err != nil || !utf8.ValidRune(rune(r)) {
				return "", false
			}
			b.WriteRune(rune(r))
			i += width
		case 'N':
			return "", false
		default:
			if c >= '0' && c <= '7' {
				end := i + 1
				for end < len(raw) && end < i+3 && raw[end] >= '0' && raw[end] <= '7' {
					end++
				}
				value, _ := strconv.ParseUint(raw[i:end], 8, 32)
				b.WriteRune(rune(value))
				i = end - 1
				continue
			}
			b.WriteByte('\\')
			b.WriteByte(c)
		}
	}
	return b.String(), true
}

func decodeJSEscapes(raw string) (string, bool) {
	var b strings.Builder
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			b.WriteByte(raw[i])
			continue
		}
		if i+1 >= len(raw) {
			return "", false
		}
		i++
		switch c := raw[i]; c {
		case '\n':
		case '\r':
			if i+1 < len(raw) && raw[i+1] == '\n' {
				i++
			}
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case 'v':
			b.WriteByte('\v')
		case '0':
			b.WriteByte(0)
		case 'x':
			if i+2 >= len(raw) {
				return "", false
			}
			r, err := strconv.ParseUint(raw[i+1:i+3], 16, 8)
			if err != nil {
				return "", false
			}
			b.WriteRune(rune(r))
			i += 2
		case 'u':
			if i+1 < len(raw) && raw[i+1] == '{' {
				end := strings.IndexByte(raw[i:], '}')
				if end < 0 {
					return "", false
				}
				r, err := strconv.ParseUint(raw[i+2:i+end], 16, 32)
				if err != nil || !utf8.ValidRune(rune(r)) {
					return "", false
				}
				b.WriteRune(rune(r))
				i += end
				continue
			}
			if i+4 >= len(raw) {
				return "", false
			}
			r, err := strconv.ParseUint(raw[i+1:i+5], 16, 32)
			if err != nil {
				return "", false
			}
			b.WriteRune(rune(r))
			i += 4
		default:
			b.WriteByte(c)
		}
	}
	return b.String(), true
}
