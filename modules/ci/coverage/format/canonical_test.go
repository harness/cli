// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package format

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/harness/cli/v3/modules/ci/coverage/env"
)

func TestCoverageRepositoryPathResolver_AmbiguousDroppedInFullFailsInPatch(t *testing.T) {
	tree := env.NewTree([]string{
		"a/com/example/Foo.java",
		"b/com/example/Foo.java",
	})

	full := NewRepositoryPathResolver(RepositoryPathOptions{Tree: tree, Format: FormatJacoco})
	choice, err := full.Resolve("com/example/Foo.java")
	if err != nil {
		t.Fatalf("full mode error = %v, want nil (ambiguous files are dropped, not failed)", err)
	}
	if choice != "" {
		t.Fatalf("full mode choice = %q, want \"\" (dropped)", choice)
	}

	patch := NewRepositoryPathResolver(RepositoryPathOptions{Tree: tree, Format: FormatJacoco, Patch: true})
	if _, err := patch.Resolve("com/example/Foo.java"); err == nil {
		t.Fatal("patch mode error = nil, want error for an ambiguous match")
	}
}

func TestCoverageRepositoryPathResolver_BasenameOnlyUniqueMatchRefused(t *testing.T) {
	tree := env.NewTree([]string{"pkg/bar.go"})

	full := NewRepositoryPathResolver(RepositoryPathOptions{Tree: tree, Format: FormatGo})
	choice, err := full.Resolve("bar.go")
	if err != nil {
		t.Fatalf("full mode error = %v", err)
	}
	if choice != "bar.go" {
		t.Fatalf("full mode choice = %q, want the stored bare name %q (basename-only match refused)", choice, "bar.go")
	}

	patch := NewRepositoryPathResolver(RepositoryPathOptions{Tree: tree, Format: FormatGo, Patch: true})
	if _, err := patch.Resolve("bar.go"); err == nil {
		t.Fatal("patch mode error = nil, want error for a basename-only match")
	}
}

func TestCoverageRepositoryPathResolver_ExcludeReducesAmbiguousSourceRootToOne(t *testing.T) {
	// Two Maven modules shadow the same package-relative path under the
	// standard src/main/java source root; matchSourceRoot must apply
	// excludes to the ambiguous suffix match before giving up.
	tree := env.NewTree([]string{
		"moduleA/src/main/java/com/example/Foo.java",
		"moduleB/src/main/java/com/example/Foo.java",
	})
	patterns, err := compileExcludePatterns([]string{"moduleA/**"})
	if err != nil {
		t.Fatalf("compileExcludePatterns() error = %v", err)
	}

	resolver := NewRepositoryPathResolver(RepositoryPathOptions{Tree: tree, Format: FormatJacoco})
	choice, err := resolver.Select("com/example/Foo.java", patterns, map[string]struct{}{}, "unused-jacoco-candidate")
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if choice.Skip || choice.Path != "moduleB/src/main/java/com/example/Foo.java" {
		t.Fatalf("choice = %+v, want moduleB/src/main/java/com/example/Foo.java (excluding moduleA/** resolves the ambiguity)", choice)
	}
}

func TestCoverageRepositoryPathResolver_CoberturaSourceCandidatesTryInOrder(t *testing.T) {
	tree := env.NewTree([]string{"src/main/pkg/Foo.cs"})
	resolver := NewRepositoryPathResolver(RepositoryPathOptions{Tree: tree, Format: FormatCobertura})

	choice, err := resolver.Select("Foo.cs", nil, nil, "other/pkg/Foo.cs", "src/main/pkg/Foo.cs")
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if choice.Path != "src/main/pkg/Foo.cs" {
		t.Fatalf("choice.Path = %q, want src/main/pkg/Foo.cs (first untracked candidate skipped)", choice.Path)
	}
}

func TestCoverageRepositoryPathResolver_ProductionSourceRootWinsOverTestRoot(t *testing.T) {
	// src/main/* layouts are listed before src/test/* in sourceRootLayouts;
	// matchSourceRoot must stop at the first non-"next" result, so a unique
	// production-root match must never fall through to the test root.
	tree := env.NewTree([]string{
		"moduleA/src/main/java/com/example/Foo.java",
		"moduleA/src/test/java/com/example/Foo.java",
	})
	resolver := NewRepositoryPathResolver(RepositoryPathOptions{Tree: tree, Format: FormatJacoco})

	choice, err := resolver.Select("com/example/Foo.java", nil, map[string]struct{}{}, "unused-jacoco-candidate")
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if choice.Skip || choice.Path != "moduleA/src/main/java/com/example/Foo.java" {
		t.Fatalf("choice = %+v, want the production source root, not the test root", choice)
	}
}

func TestCoverageRepositoryPathResolver_CoberturaExcludedSourceTriesNextCandidate(t *testing.T) {
	tree := env.NewTree([]string{"excluded/pkg/Foo.cs", "kept/pkg/Foo.cs"})
	patterns, err := compileExcludePatterns([]string{"excluded/**"})
	if err != nil {
		t.Fatalf("compileExcludePatterns() error = %v", err)
	}
	resolver := NewRepositoryPathResolver(RepositoryPathOptions{Tree: tree, Format: FormatCobertura})

	choice, err := resolver.Select("Foo.cs", patterns, map[string]struct{}{}, "excluded/pkg/Foo.cs", "kept/pkg/Foo.cs")
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if choice.Skip || choice.Path != "kept/pkg/Foo.cs" {
		t.Fatalf("choice = %+v, want kept/pkg/Foo.cs (excluded candidate skipped, next candidate tried)", choice)
	}
}

func TestCoverageRepositoryPathResolver_CoberturaAllCandidatesExcludedDropsFile(t *testing.T) {
	tree := env.NewTree([]string{"excluded/pkg/Foo.cs"})
	patterns, err := compileExcludePatterns([]string{"excluded/**"})
	if err != nil {
		t.Fatalf("compileExcludePatterns() error = %v", err)
	}
	resolver := NewRepositoryPathResolver(RepositoryPathOptions{Tree: tree, Format: FormatCobertura})

	choice, err := resolver.Select("Foo.cs", patterns, map[string]struct{}{}, "excluded/pkg/Foo.cs")
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if !choice.Skip {
		t.Fatalf("choice = %+v, want Skip:true when every tracked candidate is excluded", choice)
	}
}

func TestCoverageReadGoModuleName_StripsTrailingComment(t *testing.T) {
	root := t.TempDir()
	modPath := filepath.Join(root, "go.mod")
	if err := os.WriteFile(modPath, []byte("module example.com/app // some comment\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	name, err := readGoModuleName(modPath)
	if err != nil {
		t.Fatalf("readGoModuleName() error = %v", err)
	}
	if name != "example.com/app" {
		t.Fatalf("name = %q, want example.com/app (trailing comment stripped)", name)
	}
}

func TestCoverageRepositoryPathResolver_ManifestPrefixRejectsParentTraversal(t *testing.T) {
	tree := env.NewTree([]string{"pkg.json", "sub/pkg/foo.js"})
	resolver := &RepositoryPathResolver{
		opts:         RepositoryPathOptions{Tree: tree},
		choices:      make(map[string]cachedChoice),
		manifestRels: []string{"../escape"},
	}
	if exact, err := resolver.matchManifestPrefixes("foo.js", "foo.js", []string{"../escape"}); err != nil || exact != "" {
		t.Fatalf("matchManifestPrefixes() = (%q, %v), want (\"\", nil) when the manifest dir escapes the repo", exact, err)
	}
}

func TestCoverageRepositoryPathResolver_GoModuleResolvesImportPath(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/app\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "pkg", "foo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "foo", "bar.go"), []byte("package foo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tree := env.NewTree([]string{"go.mod", "pkg/foo/bar.go"})
	resolver := NewRepositoryPathResolver(RepositoryPathOptions{
		Tree:    tree,
		GitRoot: root,
		Format:  FormatGo,
	})

	choice, err := resolver.Resolve("example.com/app/pkg/foo/bar.go")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if choice != "pkg/foo/bar.go" {
		t.Fatalf("choice = %q, want pkg/foo/bar.go (import path resolved via go.mod module name)", choice)
	}
}
