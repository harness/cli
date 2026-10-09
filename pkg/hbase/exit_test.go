// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package hbase

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/harness/cli/v3/pkg/hlog"
)

func TestExitHooks(t *testing.T) {
	if code := os.Getenv("HARNESS_TEST_EXIT_CODE"); code != "" {
		if os.Getenv("HARNESS_TEST_EXIT_DEBUG") == "true" {
			hlog.SetDebug()
		}
		if os.Getenv("HARNESS_TEST_EXIT_FAILURE") == "true" {
			RegisterExitHook(func() error { return errors.New("cleanup failed") })
		}
		if os.Getenv("HARNESS_TEST_EXIT_PANIC") == "true" {
			RegisterExitHook(func() error { panic("cleanup panic") })
		}
		RegisterExitHook(func() error {
			_, err := os.Stdout.WriteString("cleaned up")
			return err
		})
		n, err := strconv.Atoi(code)
		if err != nil {
			t.Fatal(err)
		}
		Exit(n)
	}
	for _, tt := range []struct {
		name  string
		code  int
		fail  bool
		panic bool
		debug bool
	}{
		{"success", 0, false, false, false},
		{"close_failure", 0, true, false, false},
		{"panic", 0, false, true, false},
		{"debug_failure", 0, true, false, true},
		{"debug_panic", 0, false, true, true},
		{"timeout", TimeoutExitCode, true, true, false},
		{"command_failure", 1, true, true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestExitHooks$")
			cmd.Env = append(os.Environ(), "HARNESS_TEST_EXIT_CODE="+strconv.Itoa(tt.code),
				"HARNESS_TEST_EXIT_FAILURE="+strconv.FormatBool(tt.fail),
				"HARNESS_TEST_EXIT_PANIC="+strconv.FormatBool(tt.panic),
				"HARNESS_TEST_EXIT_DEBUG="+strconv.FormatBool(tt.debug))
			out, err := cmd.CombinedOutput()
			if cmd.ProcessState == nil {
				t.Fatalf("starting subprocess: %v", err)
			}
			if got := cmd.ProcessState.ExitCode(); got != tt.code {
				t.Fatalf("exit code = %d, want %d; error = %v; output = %s", got, tt.code, err, out)
			}
			if !strings.Contains(string(out), "cleaned up") {
				t.Fatalf("cleanup hook did not run: %s", out)
			}
			if !tt.debug && string(out) != "cleaned up" {
				t.Fatalf("unexpected output with debug disabled: %s", out)
			}
			if tt.debug {
				want := "cleanup failed"
				if tt.panic {
					want = "exit hook panicked: cleanup panic"
				}
				if !strings.Contains(string(out), "DEBUG") || !strings.Contains(string(out), want) {
					t.Fatalf("missing cleanup debug log: %s", out)
				}
			}
		})
	}
}

func TestCleanupHooksRunOnce(t *testing.T) {
	calls := 0
	wantErr := errors.New("cleanup failure")
	RegisterExitHook(func() error {
		calls++
		return wantErr
	})
	RegisterExitHook(func() error {
		calls++
		panic("cleanup panic")
	})
	RegisterExitHook(func() error {
		calls++
		return nil
	})
	if err := Cleanup(); !errors.Is(err, wantErr) {
		t.Fatalf("cleanup error = %v", err)
	} else if !strings.Contains(err.Error(), "exit hook panicked: cleanup panic") {
		t.Fatalf("missing recovered panic: %v", err)
	}
	if err := Cleanup(); err != nil {
		t.Fatalf("repeated cleanup error = %v", err)
	}
	if calls != 3 {
		t.Fatalf("hooks called %d times, want 3", calls)
	}
}
