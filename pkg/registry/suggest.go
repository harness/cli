// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"fmt"
	"sort"
	"strings"

	"github.com/harness/cli/v3/pkg/strutil"
)

// SuggestRootCommand inspects raw CLI args and returns a user-friendly error
// message when the user likely made one of these mistakes, checked in order
// from most to least specific:
//
//  1. Migrate transposition: "harness github_repo migrate ..." or "harness
//     repo export ..." instead of "harness migrate ...". Detected whenever
//     "migrate", "import", or "export" appears in the second positional slot
//     — checked before the general transposition case since migrate's noun
//     slot is a "<from>:<to>" pair that can't be reconstructed from a single
//     transposed noun, and "import"/"export" aren't real verbs at all (there's
//     only migrate).
//
//  2. Noun-verb transposition: "harness pr create" instead of "harness create pr".
//     Detected deterministically — the first positional token must be a known
//     noun (or alias), the second a known verb, and the combination must exist
//     in the registry.
//
//  3. Verb typo: "harness creaet pipeline" — the first positional token looks
//     like a mistyped verb (Levenshtein ≤ 2 from a known verb that has
//     registered commands).
//
//  4. A noun owned by a plugin that isn't installed, so it was never
//     registered and cobra can't dispatch it as either a verb or a noun.
//     Checked last since it's the fuzziest signal — a plugin-owned noun name
//     could otherwise coincidentally look like a verb typo (e.g. a
//     hypothetical noun "got" would misreport instead of suggesting "get").
//
// Returns "" when none of the above applies so the caller can fall through to
// the original error.
func (r *Registry) SuggestRootCommand(args []string) string {
	if len(args) == 0 {
		return ""
	}

	verbIdx, nounIdx := IndexVerbNoun(args)
	if verbIdx == -1 {
		return ""
	}
	first := args[verbIdx]

	if nounIdx != -1 {
		if ok, msg := r.suggestMigrateTransposition(first, args[nounIdx]); ok {
			return msg
		}
		if ok, msg := r.suggestTransposition(first, args[nounIdx]); ok {
			return msg
		}
	}
	if ok, msg := r.suggestVerbTypo(first); ok {
		return msg
	}
	if ok, msg := r.suggestPluginOwnedNoun(first); ok {
		return msg
	}
	return ""
}

// migrateVerbTypos are words a user might put in the verb slot expecting them
// to work as verbs on their own. "migrate" is the real verb, misplaced;
// "import"/"export" aren't verbs at all — migrate is the only pair verb, so
// there's no dedicated import or export command to point them at.
var migrateVerbTypos = map[string]bool{
	"migrate": true,
	"import":  true,
	"export":  true,
}

// suggestMigrateTransposition reports whether maybeVerb is "migrate",
// "import", or "export" typed in the verb slot of a noun-verb transposition
// (e.g. "harness github_repo migrate ..." or "harness repo export ...").
// Unlike suggestTransposition, it never suggests a noun: migrate takes a
// "<from>:<to>" pair that can't be reconstructed from the single transposed
// noun the user typed, so the suggestion only names the verb.
func (r *Registry) suggestMigrateTransposition(maybeNoun, maybeVerb string) (bool, string) {
	verbBase := strings.SplitN(maybeVerb, ":", 2)[0]
	if !migrateVerbTypos[strings.ToLower(verbBase)] {
		return false, ""
	}
	return true, fmt.Sprintf("unknown command %q\n\nDid you mean?\n  harness migrate ...", maybeNoun)
}

// suggestTransposition reports whether maybeNoun/maybeVerb is a noun-verb
// transposition, i.e. "harness pr create" instead of "harness create pr".
// Detected deterministically — maybeNoun must be a known noun (or alias),
// maybeVerb a known verb, and the combination must exist in the registry.
// Handles plain nouns ("pr create"), noun aliases ("prs list"), noun:variant
// ("pipeline:summary get"), and verb:variant ("pr list:mine").
func (r *Registry) suggestTransposition(maybeNoun, maybeVerb string) (bool, string) {
	// Strip :variant suffix from both positions before registry lookups.
	nounBase := strings.SplitN(maybeNoun, ":", 2)[0]
	verbBase := strings.SplitN(maybeVerb, ":", 2)[0]

	resolvedNoun := nounBase
	if canonical, ok := r.nounAliases[nounBase]; ok {
		resolvedNoun = canonical
	}
	// Reconstruct the full noun with variant for the suggestion (e.g. "pipeline:summary").
	fullNoun := resolvedNoun
	if idx := strings.Index(maybeNoun, ":"); idx >= 0 {
		fullNoun = resolvedNoun + maybeNoun[idx:]
	}

	_, nounKnown := r.nouns[resolvedNoun]
	_, verbKnown := verbRegistry[verbBase]
	if !nounKnown || !verbKnown {
		return false, ""
	}

	// Check plain noun first (e.g. "harness create pr").
	// If verb had a variant (e.g. "list:mine"), also check noun+verbVariant
	// (e.g. "harness list pr:mine" when user typed "harness pr list:mine").
	verbVariant := ""
	if idx := strings.Index(maybeVerb, ":"); idx >= 0 {
		verbVariant = maybeVerb[idx+1:]
	}
	lookupNoun := fullNoun
	if verbVariant != "" {
		lookupNoun = resolvedNoun + ":" + verbVariant
	}

	// Reconstruct the corrected command: verb before noun.
	// When verb had a variant (e.g. list:mine), move it to the noun (list pr:mine).
	// When noun had a variant (e.g. pipeline:summary), preserve it on the noun.
	suggestedNoun := fullNoun
	if verbVariant != "" {
		suggestedNoun = resolvedNoun + ":" + verbVariant
	}
	if r.GetSpec(verbBase, lookupNoun) == nil {
		// No exact verb+noun[:variant] match — some verbs (e.g. "push") only
		// register variant-specific commands ("push artifact:npm") with no
		// bare "push artifact". Fall back to whether the verb has any command
		// at all for this noun, and drop the variant from the suggestion since
		// we don't know which one the user wants; unknownNounError guides them
		// the rest of the way once they run "harness <verb> <noun>".
		if !r.nounHasCommandsForVerb(resolvedNoun, verbBase) {
			return false, ""
		}
		suggestedNoun = resolvedNoun
	}

	return true, fmt.Sprintf("unknown command %q\n\nDid you mean?\n  harness %s %s", maybeNoun, verbBase, suggestedNoun)
}

// nounHasCommandsForVerb reports whether verb has any registered command for
// noun, regardless of variant. Only meaningful for nouns already known to the
// registry (see the nounKnown check in suggestTransposition) — for those, r.specs
// is the authoritative list of every real command, installed plugin or core.
func (r *Registry) nounHasCommandsForVerb(noun, verb string) bool {
	for _, cs := range r.specs[verb] {
		if cs.Noun == noun {
			return true
		}
	}
	return false
}

// suggestVerbTypo reports whether first looks like a mistyped verb
// (Levenshtein ≤ 2 from a known verb that has registered commands).
func (r *Registry) suggestVerbTypo(first string) (bool, string) {
	bestDist := map[string]int{}
	for verb := range r.specs {
		d := strutil.Levenshtein(first, verb)
		if d <= 2 && d > 0 {
			bestDist[verb] = d
		}
	}
	if len(bestDist) == 0 {
		return false, ""
	}

	suggestions := make([]string, 0, len(bestDist))
	for v := range bestDist {
		suggestions = append(suggestions, v)
	}
	sort.Slice(suggestions, func(i, j int) bool {
		di, dj := bestDist[suggestions[i]], bestDist[suggestions[j]]
		if di != dj {
			return di < dj
		}
		return suggestions[i] < suggestions[j]
	})
	return true, fmt.Sprintf("unknown command %q\n\nDid you mean?\n  harness %s", first, strings.Join(suggestions, "\n  harness "))
}

// suggestPluginOwnedNoun reports whether first is a noun (or noun:variant)
// owned by a plugin that isn't installed, so it was never registered —
// cobra can't dispatch it as either a verb or a noun. Covers both
// "harness <noun> <verb>" and verb-less plugin verbs (e.g. har's
// push/pull/configure).
func (r *Registry) suggestPluginOwnedNoun(first string) (bool, string) {
	nounBase := strings.SplitN(first, ":", 2)[0]
	mod := r.pluginOwnerOfUnregisteredNoun(nounBase)
	if mod == "" {
		return false, ""
	}
	return true, fmt.Sprintf("%q is provided by the %q plugin, which isn't installed\n\nTo install it, run:\n  harness install plugin %s", first, mod, mod)
}
