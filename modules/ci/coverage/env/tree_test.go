// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package env

import (
	"errors"
	"testing"
)

func TestCoverageTree_ResolveWithOutcome(t *testing.T) {
	tree := NewTree([]string{
		"src/main/java/com/example/Foo.java",
		"other/com/example/Foo.java",
		"pkg/bar.go",
	})

	tests := []struct {
		name string
		path string
		kind PathResolutionKind
		want string
	}{
		{"exact tracked path", "src/main/java/com/example/Foo.java", PathResolutionExact, "src/main/java/com/example/Foo.java"},
		{"unique suffix match", "com/example/Foo.java", PathResolutionAmbiguous, ""},
		{"unique bare basename", "bar.go", PathResolutionUnique, "pkg/bar.go"},
		{"not found", "missing/file.go", PathResolutionNotFound, ""},
		{"backslash path normalized to exact", `src\main\java\com\example\Foo.java`, PathResolutionExact, "src/main/java/com/example/Foo.java"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tree.ResolveWithOutcome(tt.path)
			if got.Kind != tt.kind {
				t.Fatalf("kind = %v, want %v", got.Kind, tt.kind)
			}
			if tt.kind != PathResolutionAmbiguous && got.Path != tt.want {
				t.Fatalf("path = %q, want %q", got.Path, tt.want)
			}
		})
	}
}

func TestCoverageTree_ResolveExact_NeverDropsLeadingComponent(t *testing.T) {
	tree := NewTree([]string{"build/generated/com/example/Foo.java"})
	if got := tree.ResolveExact("com/example/Foo.java"); got != "" {
		t.Fatalf("ResolveExact(%q) = %q, want \"\" (exact match must not drop leading components)", "com/example/Foo.java", got)
	}
	if got := tree.ResolveExact("build/generated/com/example/Foo.java"); got != "build/generated/com/example/Foo.java" {
		t.Fatalf("ResolveExact full path = %q", got)
	}
}

func TestCoverageTree_Unavailable_WhenLoaderFails(t *testing.T) {
	tree := &Tree{loader: func() ([]string, error) { return nil, errors.New("boom") }}
	if tree.Available() {
		t.Fatal("Available() = true, want false when loader fails")
	}
	if got := tree.ResolveWithOutcome("foo.go"); got.Kind != PathResolutionUnavailable {
		t.Fatalf("kind = %v, want Unavailable", got.Kind)
	}
}

func TestCoverageTree_LoaderRunsOnlyOnce(t *testing.T) {
	calls := 0
	tree := &Tree{loader: func() ([]string, error) {
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
	tree := &Tree{loader: func() ([]string, error) { return nil, nil }}
	if !tree.Available() {
		t.Fatal("Available() = false, want true for an empty-but-successful load")
	}
	if got := tree.ResolveWithOutcome("foo.go"); got.Kind != PathResolutionNotFound {
		t.Fatalf("kind = %v, want NotFound", got.Kind)
	}
}

func TestCoverageTree_ResolveExact_WrongModulePrefixDoesNotMatch(t *testing.T) {
	tree := NewTree([]string{"moduleA/pkg/bar.go"})
	if got := tree.ResolveExact("moduleB/pkg/bar.go"); got != "" {
		t.Fatalf("ResolveExact() = %q, want \"\" (different leading component must not match)", got)
	}
}

func TestCoverageTree_ResolveExact_DuplicatePathStillMatches(t *testing.T) {
	tree := NewTree([]string{"pkg/bar.go", "pkg/bar.go"})
	if got := tree.ResolveExact("pkg/bar.go"); got != "pkg/bar.go" {
		t.Fatalf("ResolveExact() = %q, want pkg/bar.go (identical duplicate entries are not ambiguous)", got)
	}
}
