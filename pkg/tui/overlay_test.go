// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"
	"testing"

	"github.com/harness/cli/v3/pkg/console"
)

func TestOverlayCenter(t *testing.T) {
	bg := strings.Join([]string{"aaaaaaaaaa", "bbbbbbbbbb", "cccccccccc", "dddddddddd"}, "\n")
	got := console.StripANSI(OverlayCenter(bg, "XX\nYY", 10, 4))
	want := strings.Join([]string{"aaaaaaaaaa", "bbbbXX", "ccccYY", "dddddddddd"}, "\n")
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestOverlayCenterKeepsColoredPrefixIntact(t *testing.T) {
	bg := "\x1b[31mredredred\x1b[0m\n\x1b[31mredredred\x1b[0m"
	got := OverlayCenter(bg, "X", 9, 2)
	if plain := console.StripANSI(got); plain != "redrX\nredredred" {
		t.Fatalf("got %q", plain)
	}
	if !strings.Contains(got, "\x1b[0mX") {
		t.Fatalf("box must start after a color reset: %q", got)
	}
}

func TestOverlayCenterPadsShortBackgroundLine(t *testing.T) {
	got := console.StripANSI(OverlayCenter("a\n\n", "X", 10, 3))
	if got != "a\n    X\n" {
		t.Fatalf("got %q", got)
	}
}

func TestOverlayCenterPadsShortBackground(t *testing.T) {
	got := console.StripANSI(OverlayCenter("top", "X\nY", 10, 6))
	if got != "top\n\n    X\n    Y\n\n" {
		t.Fatalf("got %q", got)
	}
}
