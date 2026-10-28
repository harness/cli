// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package format

import (
	"encoding/xml"
	"io"
	"iter"
	"path"
	"strconv"
	"strings"
)

// jacocoParser decodes JaCoCo XML coverage reports.
type jacocoParser struct {
	project string
	err     error
}

func newJacocoParser() *jacocoParser {
	return &jacocoParser{}
}

func (p *jacocoParser) Err() error { return p.err }

type pathResolution struct {
	path       string
	candidates []string
}

var sourceRootLayouts = []string{
	"",
	"src/main/java",
	"src/main/kotlin",
	"src/main/scala",
	"src/main/groovy",
	"src/test/java",
	"src/test/kotlin",
	"src/test/scala",
	"src/test/groovy",
}

// sourceRootSuffixes returns filePath joined under each of the common
// Maven/Gradle source-root layouts, so callers have candidate repo-relative
// paths to try even without a module qualifier.
func sourceRootSuffixes(filePath string) []string {
	if filePath == "" {
		return nil
	}
	suffixes := make([]string, 0, len(sourceRootLayouts))
	for _, layout := range sourceRootLayouts {
		if layout == "" {
			suffixes = append(suffixes, filePath)
			continue
		}
		suffixes = append(suffixes, path.Join(layout, filePath))
	}
	return suffixes
}

func containsParent(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

// normalizeProject sanitizes a JaCoCo <report>/<group> name attribute
// into a path-safe qualifier, mapping Gradle's ":app:module" convention onto
// "app/module".
func normalizeProject(name string) string {
	if strings.Contains(name, " ") {
		return ""
	}
	trimmed := strings.ReplaceAll(name, `\`, "/")
	trimmed = strings.Trim(trimmed, "/")
	trimmed = strings.TrimPrefix(trimmed, ":")
	trimmed = strings.ReplaceAll(trimmed, ":", "/")
	if containsParent(trimmed) {
		return ""
	}
	return trimmed
}

func normalizePath(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	if containsParent(p) {
		return ""
	}
	p = strings.TrimPrefix(p, "./")
	p = strings.TrimPrefix(p, "/")
	return path.Clean(p)
}

func currentQualifier(project string, groupStack []string) string {
	var nonEmpty []string
	for _, g := range groupStack {
		if g != "" {
			nonEmpty = append(nonEmpty, g)
		}
	}
	if len(nonEmpty) == 0 {
		return project
	}
	return path.Join(nonEmpty...)
}

// exactCandidates lists the repo-relative paths filePath could
// resolve to: the bare source-root suffixes, plus the same suffixes prefixed
// by qualifier when qualifier is a usable (non-empty, non-"..") path.
func exactCandidates(qualifier, filePath string) []string {
	suffixes := sourceRootSuffixes(filePath)
	if qualifier == "" || containsParent(qualifier) {
		return suffixes
	}
	candidates := make([]string, 0, len(suffixes)*2)
	seen := make(map[string]bool, len(suffixes)*2)
	for _, s := range suffixes {
		if !seen[s] {
			seen[s] = true
			candidates = append(candidates, s)
		}
	}
	for _, s := range suffixes {
		qualified := path.Join(qualifier, s)
		if !seen[qualified] {
			seen[qualified] = true
			candidates = append(candidates, qualified)
		}
	}
	return candidates
}

func jacocoAttr(se xml.StartElement, name string) string {
	for _, a := range se.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

func jacocoAtoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

func (p *jacocoParser) Parse(reader io.Reader) (iter.Seq[LineRecord], error) {
	return func(yield func(LineRecord) bool) {
		decoder := xml.NewDecoder(reader)
		var pkgName string
		var groupStack []string
		var current pathResolution
		insideSourceFile := false
		resolved := make(map[string]pathResolution)

		for {
			tok, err := decoder.Token()
			if err == io.EOF {
				return
			}
			if err != nil {
				p.err = err
				return
			}
			switch se := tok.(type) {
			case xml.StartElement:
				switch se.Name.Local {
				case "report":
					p.project = normalizeProject(jacocoAttr(se, "name"))
				case "group":
					groupStack = append(groupStack, normalizeProject(jacocoAttr(se, "name")))
				case "package":
					pkgName = jacocoAttr(se, "name")
				case "sourcefile":
					insideSourceFile = true
					filePath := normalizePath(path.Join(pkgName, jacocoAttr(se, "name")))
					qualifier := currentQualifier(p.project, groupStack)
					cacheKey := qualifier + "\x00" + filePath
					if cached, ok := resolved[cacheKey]; ok {
						current = cached
					} else {
						current = pathResolution{path: filePath, candidates: exactCandidates(qualifier, filePath)}
						resolved[cacheKey] = current
					}
				case "line":
					if !insideSourceFile || current.path == "" {
						continue
					}
					ln := jacocoAtoi(jacocoAttr(se, "nr"))
					if ln <= 0 {
						continue
					}
					mi := jacocoAtoi(jacocoAttr(se, "mi"))
					ci := jacocoAtoi(jacocoAttr(se, "ci"))
					mb := jacocoAtoi(jacocoAttr(se, "mb"))
					cb := jacocoAtoi(jacocoAttr(se, "cb"))
					if mi == 0 && ci == 0 && mb == 0 && cb == 0 {
						continue
					}
					var lineType LineType
					switch {
					case mb > 0 || cb > 0:
						switch {
						case cb == 0:
							lineType = LineUncovered
						case mb == 0:
							lineType = LineCovered
						default:
							lineType = LinePartial
						}
					case ci > 0 && mi == 0:
						lineType = LineCovered
					case ci == 0 && mi > 0:
						lineType = LineUncovered
					default:
						lineType = LinePartial
					}
					if !yield(LineRecord{
						FilePath:       current.path,
						LineNumber:     uint32(ln),
						LineType:       lineType,
						PathCandidates: current.candidates,
					}) {
						return
					}
				}
			case xml.EndElement:
				switch se.Name.Local {
				case "sourcefile":
					insideSourceFile = false
					current = pathResolution{}
				case "group":
					if len(groupStack) > 0 {
						groupStack = groupStack[:len(groupStack)-1]
					}
				}
			}
		}
	}, nil
}
