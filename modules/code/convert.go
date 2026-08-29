// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package code

import (
	"fmt"
	"strconv"
	"time"
)

// asString, asInt64, asMap, asBool, and asSlice pull typed values out of the
// map[string]any produced by decoding raw API JSON, defaulting to the zero
// value when the key is absent or holds an unexpected type.

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func asInt64(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case string:
		i, _ := strconv.ParseInt(n, 10, 64)
		return i
	}
	return 0
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func asBool(v any) bool {
	b, _ := v.(bool)
	return b
}

func asSlice(v any) []any {
	s, _ := v.([]any)
	return s
}

// relativeTime renders an epoch-ms timestamp as a short relative duration
// ("2h ago"), falling back to an absolute date once the "N units ago" phrasing
// stops being useful (past a month). Returns "unknown" for a zero/missing timestamp.
func relativeTime(ms int64) string {
	if ms <= 0 {
		return "unknown"
	}
	d := time.Since(time.UnixMilli(ms))
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return time.UnixMilli(ms).UTC().Format("2006-01-02")
	}
}
