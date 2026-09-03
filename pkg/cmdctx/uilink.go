// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package cmdctx

// UIScreenKind identifies which screen function should be called to redraw a
// UILink: a browse table scoped to a parent id, a browse table scoped to a
// parent id that itself came from a "get" lookup, or a detail-only overlay.
type UIScreenKind int

const (
	ScreenTable UIScreenKind = iota
	ScreenTableForGet
	ScreenDetailForGet
)

// UILink is a replayable reference to a --ui Hop's target: enough to rebuild
// the Ctx for that screen and redraw it, without encoding anything onto a
// command line. Profile/Org/Project are the raw scope flag strings in effect
// at the Hop (not a cached resolved Auth), so replay can re-resolve auth
// instead of inheriting whatever was live in memory when the Link was pushed.
type UILink struct {
	Verb, Noun string
	Id         string
	IdParts    []string
	Level      string

	Profile, Org, Project string
	FlagValues            map[string]any

	Screen  UIScreenKind
	ListPos int
}
