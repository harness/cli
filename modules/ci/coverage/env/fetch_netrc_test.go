// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package env

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNetrcGitArgs(t *testing.T) {
	tests := []struct {
		name                string
		machine, user, pass string
		wantArgs            bool
	}{
		{"all set", "github.com", "bob", "s3cret-pw", true},
		{"missing password", "github.com", "u", "", false},
		{"missing username", "github.com", "", "s3cret-pw", false},
		{"unset", "", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DRONE_NETRC_MACHINE", tt.machine)
			t.Setenv("DRONE_NETRC_USERNAME", tt.user)
			t.Setenv("DRONE_NETRC_PASSWORD", tt.pass)
			got := netrcGitArgs()
			if (got != nil) != tt.wantArgs {
				t.Fatalf("netrcGitArgs() = %v, wantArgs %v", got, tt.wantArgs)
			}
			for _, a := range got {
				if tt.pass != "" && strings.Contains(a, tt.pass) {
					t.Errorf("secret leaked into git args: %q", a)
				}
			}
		})
	}
}

// The helper must emit the password byte-for-byte; echo under dash expands backslash escapes.
func TestNetrcGitArgs_HelperPreservesBackslashes(t *testing.T) {
	t.Setenv("DRONE_NETRC_MACHINE", "github.com")
	t.Setenv("DRONE_NETRC_USERNAME", "bob")
	t.Setenv("DRONE_NETRC_PASSWORD", `a\nb\tc\\d`)
	args := netrcGitArgs()
	if len(args) != 4 || args[2] != "-c" {
		t.Fatalf("unexpected args: %v", args)
	}
	helper := strings.SplitN(args[3], "=", 2)
	if want := "credential.https://github.com.helper"; helper[0] != want {
		t.Fatalf("config key = %q, want %q", helper[0], want)
	}
	for _, sh := range []string{"sh", "dash"} {
		if _, err := exec.LookPath(sh); err != nil {
			continue
		}
		out, err := exec.Command(sh, "-c", strings.TrimPrefix(helper[1], "!")).Output()
		if err != nil {
			t.Fatalf("%s: helper failed: %v", sh, err)
		}
		want := "username=bob\npassword=" + `a\nb\tc\\d` + "\n"
		if string(out) != want {
			t.Errorf("%s: helper output = %q, want %q", sh, out, want)
		}
	}
}

// setupCloneWithNewOriginBranch returns a clone whose origin has branch "feature" that the clone has not fetched.
func setupCloneWithNewOriginBranch(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	origin := filepath.Join(root, "origin")
	work := filepath.Join(root, "work")
	run(root, "init", "-q", "-b", "main", origin)
	run(origin, "commit", "-q", "--allow-empty", "-m", "init")
	run(root, "clone", "-q", origin, work)
	run(origin, "branch", "feature")
	t.Chdir(work)
}

func TestEnsureRefFetched(t *testing.T) {
	t.Setenv("DRONE_NETRC_MACHINE", "")
	t.Setenv("DRONE_NETRC_USERNAME", "")
	t.Setenv("DRONE_NETRC_PASSWORD", "")

	t.Run("existing local ref is returned as-is", func(t *testing.T) {
		setupCloneWithNewOriginBranch(t)
		if got := EnsureRefFetched("main"); got != "main" {
			t.Errorf("got %q, want main", got)
		}
	})
	t.Run("bare name is fetched and prefixed with origin/", func(t *testing.T) {
		setupCloneWithNewOriginBranch(t)
		if got := EnsureRefFetched("feature"); got != "origin/feature" {
			t.Errorf("got %q, want origin/feature", got)
		}
		if !RefExists("origin/feature") {
			t.Error("origin/feature should exist after fetch")
		}
	})
	t.Run("origin/ prefixed name is fetched", func(t *testing.T) {
		setupCloneWithNewOriginBranch(t)
		if got := EnsureRefFetched("origin/feature"); got != "origin/feature" {
			t.Errorf("got %q, want origin/feature", got)
		}
	})
	for _, pw := range []string{"", "s3cret-pw"} {
		t.Run("failed fetch returns original ref, password="+pw, func(t *testing.T) {
			setupCloneWithNewOriginBranch(t)
			if pw != "" {
				// scoped to https://github.com, so it is unused for the local file origin
				t.Setenv("DRONE_NETRC_MACHINE", "github.com")
				t.Setenv("DRONE_NETRC_USERNAME", "bob")
				t.Setenv("DRONE_NETRC_PASSWORD", pw)
			}
			if got := EnsureRefFetched("does-not-exist"); got != "does-not-exist" {
				t.Errorf("got %q, want original ref", got)
			}
		})
	}
}
