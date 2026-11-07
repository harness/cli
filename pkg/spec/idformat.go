// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package spec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// IdFormat is one parsed id_formats entry, e.g. "<registry>/<name...>/<version>".
type IdFormat struct {
	Raw   string
	Parts []IdFormatPart
}

// IdFormatPart is one "/"-separated part of an IdFormat. A Rest part ("<name...>")
// absorbs every segment not claimed by the fixed parts around it.
type IdFormatPart struct {
	Name string
	Rest bool
}

// ParsedId is the result of matching a positional id against a command's id_formats.
type ParsedId struct {
	Format *IdFormat
	Values []string
	// Ids maps every part name declared by any format to its value; names not
	// in the matched format map to "".
	Ids map[string]string
	// RestSegments is how many plain-form segments the Rest part absorbed (0 for
	// the JSON form or a format without a Rest part).
	RestSegments int
}

var idFormatNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ParseIdFormat parses a single id_formats entry.
func ParseIdFormat(raw string) (IdFormat, error) {
	f := IdFormat{Raw: raw}
	if raw == "" {
		return f, fmt.Errorf("empty format")
	}
	seen := map[string]bool{}
	for _, seg := range strings.Split(raw, "/") {
		inner, ok := strings.CutPrefix(seg, "<")
		if ok {
			inner, ok = strings.CutSuffix(inner, ">")
		}
		if !ok {
			return f, fmt.Errorf("format %q: part %q must be <name> or <name...>", raw, seg)
		}
		name, rest := strings.CutSuffix(inner, "...")
		if !idFormatNameRe.MatchString(name) {
			return f, fmt.Errorf("format %q: invalid part name %q", raw, name)
		}
		if seen[name] {
			return f, fmt.Errorf("format %q: duplicate part name %q", raw, name)
		}
		seen[name] = true
		if rest && f.restIndex() >= 0 {
			return f, fmt.Errorf("format %q: at most one <name...> part is allowed", raw)
		}
		f.Parts = append(f.Parts, IdFormatPart{Name: name, Rest: rest})
	}
	return f, nil
}

// ParseIdFormats parses an id_formats list and rejects sets where one input
// could match more than one format.
func ParseIdFormats(raws []string) ([]IdFormat, error) {
	formats := make([]IdFormat, 0, len(raws))
	for _, raw := range raws {
		f, err := ParseIdFormat(raw)
		if err != nil {
			return nil, err
		}
		formats = append(formats, f)
	}
	for i, a := range formats {
		for _, b := range formats[i+1:] {
			if formatsOverlap(a, b) {
				return nil, fmt.Errorf("formats %q and %q are ambiguous: both accept ids with the same number of parts", a.Raw, b.Raw)
			}
		}
	}
	return formats, nil
}

func formatsOverlap(a, b IdFormat) bool {
	na, nb := len(a.Parts), len(b.Parts)
	switch ra, rb := a.restIndex() >= 0, b.restIndex() >= 0; {
	case ra && rb:
		return true
	case ra:
		return nb >= na
	case rb:
		return na >= nb
	default:
		return na == nb
	}
}

func (f *IdFormat) restIndex() int {
	return slices.IndexFunc(f.Parts, func(p IdFormatPart) bool { return p.Rest })
}

func (f *IdFormat) acceptsSegments(n int) bool {
	if f.restIndex() >= 0 {
		return n >= len(f.Parts)
	}
	return n == len(f.Parts)
}

// FormatsLabel renders formats for usage text, e.g. "<pipeline>/<execution> | <execution>".
func FormatsLabel(formats []IdFormat) string {
	raws := make([]string, len(formats))
	for i, f := range formats {
		raws[i] = f.Raw
	}
	return strings.Join(raws, " | ")
}

func isJSONIdForm(input string) bool {
	t := strings.TrimSpace(input)
	return strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]")
}

// ParseId matches input against formats. Input whose trimmed form is wrapped in
// "[...]" must be a JSON array of strings or numbers; it never falls back to the
// plain "/"-separated form.
func ParseId(formats []IdFormat, input string) (*ParsedId, error) {
	if isJSONIdForm(input) {
		return parseJSONId(formats, input)
	}
	segs := strings.Split(input, "/")
	for i := range formats {
		f := &formats[i]
		if !f.acceptsSegments(len(segs)) {
			continue
		}
		p := &ParsedId{Format: f, Values: make([]string, len(f.Parts))}
		ri := f.restIndex()
		if ri < 0 {
			copy(p.Values, segs)
		} else {
			tail := len(f.Parts) - ri - 1
			copy(p.Values, segs[:ri])
			p.RestSegments = len(segs) - ri - tail
			p.Values[ri] = strings.Join(segs[ri:ri+p.RestSegments], "/")
			copy(p.Values[ri+1:], segs[len(segs)-tail:])
		}
		p.Ids = idsMap(formats, f, p.Values)
		return p, nil
	}
	return nil, fmt.Errorf("id %q does not match %s", input, FormatsLabel(formats))
}

func parseJSONId(formats []IdFormat, input string) (*ParsedId, error) {
	dec := json.NewDecoder(strings.NewReader(input))
	dec.UseNumber()
	var raw []any
	if err := dec.Decode(&raw); err != nil || dec.More() {
		return nil, fmt.Errorf("invalid JSON id %q: must be a JSON array of strings, e.g. '[\"a\",\"b/c\"]'", input)
	}
	values := make([]string, len(raw))
	for i, v := range raw {
		switch v := v.(type) {
		case string:
			values[i] = v
		case json.Number:
			values[i] = v.String()
		default:
			return nil, fmt.Errorf("invalid JSON id %q: element %d must be a string or number", input, i)
		}
	}
	for i := range formats {
		f := &formats[i]
		if len(f.Parts) == len(values) {
			return &ParsedId{Format: f, Values: values, Ids: idsMap(formats, f, values)}, nil
		}
	}
	return nil, fmt.Errorf("JSON id %q has %d parts, which does not match %s", input, len(values), FormatsLabel(formats))
}

func idsMap(formats []IdFormat, matched *IdFormat, values []string) map[string]string {
	ids := map[string]string{}
	for _, f := range formats {
		for _, p := range f.Parts {
			ids[p.Name] = ""
		}
	}
	for i, p := range matched.Parts {
		ids[p.Name] = values[i]
	}
	return ids
}

// AmbiguityHint describes how a plain-form id was split when its format has a
// Rest part, since a Rest part that absorbed extra segments and a missing part
// that shifted a segment into a fixed part both look valid. It returns "" for
// JSON-form ids and formats without a Rest part.
func (p *ParsedId) AmbiguityHint(input string) string {
	if p == nil || p.RestSegments == 0 {
		return ""
	}
	var parts []string
	for i, fp := range p.Format.Parts {
		parts = append(parts, fmt.Sprintf("%s=%q", fp.Name, p.Values[i]))
	}
	return fmt.Sprintf("hint: id %q was read as %s (format %s); if that is wrong, check that no part is missing, or pass the parts exactly as a JSON array, e.g. '%s'",
		input, strings.Join(parts, " "), p.Format.Raw, idJSON(p.Values))
}

// IdJoin builds an id from part values in format order. It emits the plain
// "/"-separated form when that parses back to the same parts under formats, and
// the compact JSON array form otherwise. With no formats, the plain form is
// used only when no part is empty or contains "/".
func IdJoin(formats []IdFormat, values ...string) string {
	plain := strings.Join(values, "/")
	if len(formats) == 0 {
		if !isJSONIdForm(plain) && !slices.ContainsFunc(values, func(v string) bool { return v == "" || strings.Contains(v, "/") }) {
			return plain
		}
		return idJSON(values)
	}
	if p, err := ParseId(formats, plain); err == nil && slices.Equal(p.Values, values) {
		return plain
	}
	return idJSON(values)
}

func idJSON(values []string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if values == nil {
		values = []string{}
	}
	_ = enc.Encode(values)
	return strings.TrimSuffix(buf.String(), "\n")
}
