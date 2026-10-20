// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package ci

import (
	"errors"
	"testing"
)

func TestCoverageTree_ResolveWithOutcome(t *testing.T) {
	tree := newCoverageTree([]string{
		"src/main/java/com/example/Foo.java",
		"other/com/example/Foo.java",
		"pkg/bar.go",
	})

	tests := []struct {
		name string
		path string
		kind coveragePathResolutionKind
		want string
	}{
		{"exact tracked path", "src/main/java/com/example/Foo.java", coveragePathResolutionExact, "src/main/java/com/example/Foo.java"},
		{"unique suffix match", "com/example/Foo.java", coveragePathResolutionAmbiguous, ""},
		{"unique bare basename", "bar.go", coveragePathResolutionUnique, "pkg/bar.go"},
		{"not found", "missing/file.go", coveragePathResolutionNotFound, ""},
		{"backslash path normalized to exact", `src\main\java\com\example\Foo.java`, coveragePathResolutionExact, "src/main/java/com/example/Foo.java"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tree.ResolveWithOutcome(tt.path)
			if got.kind != tt.kind {
				t.Fatalf("kind = %v, want %v", got.kind, tt.kind)
			}
			if tt.kind != coveragePathResolutionAmbiguous && got.path != tt.want {
				t.Fatalf("path = %q, want %q", got.path, tt.want)
			}
		})
	}
}

func TestCoverageTree_ResolveExact_NeverDropsLeadingComponent(t *testing.T) {
	tree := newCoverageTree([]string{"build/generated/com/example/Foo.java"})
	if got := tree.ResolveExact("com/example/Foo.java"); got != "" {
		t.Fatalf("ResolveExact(%q) = %q, want \"\" (exact match must not drop leading components)", "com/example/Foo.java", got)
	}
	if got := tree.ResolveExact("build/generated/com/example/Foo.java"); got != "build/generated/com/example/Foo.java" {
		t.Fatalf("ResolveExact full path = %q", got)
	}
}

func TestCoverageTree_Unavailable_WhenLoaderFails(t *testing.T) {
	tree := &coverageTree{loader: func() ([]string, error) { return nil, errors.New("boom") }}
	if tree.Available() {
		t.Fatal("Available() = true, want false when loader fails")
	}
	if got := tree.ResolveWithOutcome("foo.go"); got.kind != coveragePathResolutionUnavailable {
		t.Fatalf("kind = %v, want Unavailable", got.kind)
	}
}

func TestCoverageTree_LoaderRunsOnlyOnce(t *testing.T) {
	calls := 0
	tree := &coverageTree{loader: func() ([]string, error) {
		calls++
		return []string{"pkg/bar.go"}, nil
	}}
	for i := 0; i < 3; i++ {
		if !tree.Available() {
			t.Fatalf("Available() = false on call %d", i)
		}
	}
	if calls != 1 {
		t.Fatalf("loader called %d times, want 1 (sync.Once must not re-run after success)", calls)
	}
}

func TestCoverageTree_EmptyInventoryIsAvailable(t *testing.T) {
	tree := &coverageTree{loader: func() ([]string, error) { return nil, nil }}
	if !tree.Available() {
		t.Fatal("Available() = false, want true for an empty-but-successful load")
	}
	if got := tree.ResolveWithOutcome("foo.go"); got.kind != coveragePathResolutionNotFound {
		t.Fatalf("kind = %v, want NotFound", got.kind)
	}
}

func TestCoverageTree_ResolveExact_WrongModulePrefixDoesNotMatch(t *testing.T) {
	tree := newCoverageTree([]string{"moduleA/pkg/bar.go"})
	if got := tree.ResolveExact("moduleB/pkg/bar.go"); got != "" {
		t.Fatalf("ResolveExact() = %q, want \"\" (different leading component must not match)", got)
	}
}

func TestCoverageTree_ResolveExact_DuplicatePathStillMatches(t *testing.T) {
	tree := newCoverageTree([]string{"pkg/bar.go", "pkg/bar.go"})
	if got := tree.ResolveExact("pkg/bar.go"); got != "pkg/bar.go" {
		t.Fatalf("ResolveExact() = %q, want pkg/bar.go (identical duplicate entries are not ambiguous)", got)
	}
}
