// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package ci

import (
	"encoding/xml"
	"io"
	"iter"
	"path"
	"strconv"
	"strings"
)

// coverageJacocoParser decodes JaCoCo XML coverage reports.
type coverageJacocoParser struct {
	project string
	err     error
}

func newCoverageJacocoParser() *coverageJacocoParser {
	return &coverageJacocoParser{}
}

func (p *coverageJacocoParser) Err() error { return p.err }

type coveragePathResolution struct {
	path       string
	candidates []string
}

var coverageSourceRootLayouts = []string{
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

// coverageSourceRootSuffixes returns filePath joined under each of the common
// Maven/Gradle source-root layouts, so callers have candidate repo-relative
// paths to try even without a module qualifier.
func coverageSourceRootSuffixes(filePath string) []string {
	suffixes := make([]string, 0, len(coverageSourceRootLayouts))
	for _, layout := range coverageSourceRootLayouts {
		if layout == "" {
			suffixes = append(suffixes, filePath)
			continue
		}
		suffixes = append(suffixes, path.Join(layout, filePath))
	}
	return suffixes
}

func coverageContainsParent(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

// coverageNormalizeProject sanitizes a JaCoCo <report>/<group> name attribute
// into a path-safe qualifier, mapping Gradle's ":app:module" convention onto
// "app/module".
func coverageNormalizeProject(name string) string {
	if strings.Contains(name, " ") {
		return ""
	}
	trimmed := strings.ReplaceAll(name, `\`, "/")
	trimmed = strings.Trim(trimmed, "/")
	trimmed = strings.TrimPrefix(trimmed, ":")
	trimmed = strings.ReplaceAll(trimmed, ":", "/")
	if coverageContainsParent(trimmed) {
		return ""
	}
	return trimmed
}

func coverageNormalizePath(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	if coverageContainsParent(p) {
		return ""
	}
	p = strings.TrimPrefix(p, "./")
	p = strings.TrimPrefix(p, "/")
	return path.Clean(p)
}

func coverageCurrentQualifier(project string, groupStack []string) string {
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

// coverageExactCandidates lists the repo-relative paths filePath could
// resolve to: the bare source-root suffixes, plus the same suffixes prefixed
// by qualifier when qualifier is a usable (non-empty, non-"..") path.
func coverageExactCandidates(qualifier, filePath string) []string {
	suffixes := coverageSourceRootSuffixes(filePath)
	if qualifier == "" || coverageContainsParent(qualifier) {
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

func coverageJacocoAttr(se xml.StartElement, name string) string {
	for _, a := range se.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

func coverageJacocoAtoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

func (p *coverageJacocoParser) Parse(reader io.Reader) (iter.Seq[coverageLineRecord], error) {
	return func(yield func(coverageLineRecord) bool) {
		decoder := xml.NewDecoder(reader)
		var pkgName string
		var groupStack []string
		var current coveragePathResolution
		insideSourceFile := false
		resolved := make(map[string]coveragePathResolution)

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
					p.project = coverageNormalizeProject(coverageJacocoAttr(se, "name"))
				case "group":
					groupStack = append(groupStack, coverageNormalizeProject(coverageJacocoAttr(se, "name")))
				case "package":
					pkgName = coverageJacocoAttr(se, "name")
				case "sourcefile":
					insideSourceFile = true
					filePath := coverageNormalizePath(path.Join(pkgName, coverageJacocoAttr(se, "name")))
					qualifier := coverageCurrentQualifier(p.project, groupStack)
					cacheKey := qualifier + "\x00" + filePath
					if cached, ok := resolved[cacheKey]; ok {
						current = cached
					} else {
						current = coveragePathResolution{path: filePath, candidates: coverageExactCandidates(qualifier, filePath)}
						resolved[cacheKey] = current
					}
				case "line":
					if !insideSourceFile || current.path == "" {
						continue
					}
					ln := coverageJacocoAtoi(coverageJacocoAttr(se, "nr"))
					if ln <= 0 {
						continue
					}
					mi := coverageJacocoAtoi(coverageJacocoAttr(se, "mi"))
					ci := coverageJacocoAtoi(coverageJacocoAttr(se, "ci"))
					mb := coverageJacocoAtoi(coverageJacocoAttr(se, "mb"))
					cb := coverageJacocoAtoi(coverageJacocoAttr(se, "cb"))
					if mi == 0 && ci == 0 && mb == 0 && cb == 0 {
						continue
					}
					var lineType coverageLineType
					switch {
					case mb > 0 || cb > 0:
						switch {
						case cb == 0:
							lineType = coverageLineUncovered
						case mb == 0:
							lineType = coverageLineCovered
						default:
							lineType = coverageLinePartial
						}
					case ci > 0 && mi == 0:
						lineType = coverageLineCovered
					case ci == 0 && mi > 0:
						lineType = coverageLineUncovered
					default:
						lineType = coverageLinePartial
					}
					if !yield(coverageLineRecord{
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
					current = coveragePathResolution{}
				case "group":
					if len(groupStack) > 0 {
						groupStack = groupStack[:len(groupStack)-1]
					}
				}
			}
		}
	}, nil
}
