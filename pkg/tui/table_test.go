// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package tui

import "testing"

func rowsN(n int) []Row {
	rows := make([]Row, n)
	for i := range rows {
		rows[i] = Row{"x"}
	}
	return rows
}

func TestSetCursor_WithinRange(t *testing.T) {
	tm := NewTable(nil, 5, 40)
	tm.SetRows(rowsN(10))
	tm.SetCursor(3)
	if got := tm.Cursor(); got != 3 {
		t.Fatalf("Cursor() = %d, want 3", got)
	}
}

func TestSetCursor_NegativeClampsToZero(t *testing.T) {
	tm := NewTable(nil, 5, 40)
	tm.SetRows(rowsN(10))
	tm.SetCursor(-5)
	if got := tm.Cursor(); got != 0 {
		t.Fatalf("Cursor() = %d, want 0", got)
	}
}

func TestSetCursor_OutOfRangeClampsToLast(t *testing.T) {
	tm := NewTable(nil, 5, 40)
	tm.SetRows(rowsN(10))
	tm.SetCursor(100)
	if got := tm.Cursor(); got != 9 {
		t.Fatalf("Cursor() = %d, want 9 (last row)", got)
	}
}

func TestSetCursor_EmptyRows(t *testing.T) {
	tm := NewTable(nil, 5, 40)
	tm.SetCursor(3)
	if got := tm.Cursor(); got != 0 {
		t.Fatalf("Cursor() = %d, want 0 (no rows)", got)
	}
}

func TestSetCursor_ScrollFollowsCursor(t *testing.T) {
	tm := NewTable(nil, 5, 40) // height=5 visible rows
	tm.SetRows(rowsN(20))
	tm.SetCursor(15)
	if tm.scroll == 0 {
		t.Fatal("scroll = 0, want scrolled down to keep cursor in view")
	}
	if tm.cursor < tm.scroll || tm.cursor >= tm.scroll+tm.height {
		t.Fatalf("cursor %d not within visible window [%d, %d)", tm.cursor, tm.scroll, tm.scroll+tm.height)
	}
}
