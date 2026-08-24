// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"strings"

	"github.com/spf13/pflag"
)

type flagKind int

const (
	flagKindString flagKind = iota
	flagKindBool
	flagKindInt
)

type flagSpec struct {
	name    string
	short   string
	defStr  string
	defInt  int
	defBool bool
	usage   string
	kind    flagKind
}

var (
	specFormat      = flagSpec{name: "format", usage: "Output format: json, yaml, jsonl, table, csv, tsv"}
	specJson        = flagSpec{name: "json", kind: flagKindBool, usage: "Output as JSON (shorthand for --format json)"}
	specYaml        = flagSpec{name: "yaml", kind: flagKindBool, usage: "Output as YAML (shorthand for --format yaml)"}
	specColumns     = flagSpec{name: "columns", usage: `Columns to display by ID or expr, e.g. "name,org" or "+sparkline" or "Name:it.name"`}
	specNoHeaders   = flagSpec{name: "no-headers", kind: flagKindBool, usage: "Suppress column headers (table/csv/tsv) and paging footer (table)"}
	specOut         = flagSpec{name: "out", short: "o", usage: "Write output to file instead of stdout"}
	specRaw         = flagSpec{name: "raw", kind: flagKindBool, usage: "Output the full raw API response (only with --format json)"}
	specFile        = flagSpec{name: "file", short: "f", usage: "Read request body from file, or - for stdin"}
	specListColumns = flagSpec{name: "list-columns", kind: flagKindBool, usage: "Print available column IDs and exit (use with --columns to customize output)"}
	specFields      = flagSpec{name: "fields", usage: `Fields to extract, tab-separated on one line, e.g. "name" or "name,git_url"`}
	specListFields  = flagSpec{name: "list-fields", kind: flagKindBool, usage: "Print available field IDs and exit (use with --fields to customize output)"}
	specPage        = flagSpec{name: "page", kind: flagKindInt, defInt: 1, usage: "Page number (1-indexed)"}
	specLevel       = flagSpec{name: "level", defStr: "", usage: "Scope level: project, org, or account (overrides prefix on id)"}
	specOffset      = flagSpec{name: "offset", kind: flagKindInt, usage: "Skip the first N items (item-level)"}
	specLimit       = flagSpec{name: "limit", kind: flagKindInt, usage: "Return at most N items"}
	specAll         = flagSpec{name: "all", kind: flagKindBool, usage: "Fetch all pages (incompatible with --offset and --limit)"}
	specCount       = flagSpec{name: "count", kind: flagKindBool, usage: "Print total item count and exit (incompatible with --offset, --limit, --all)"}
	specUI          = flagSpec{name: "ui", kind: flagKindBool, usage: "Launch interactive TUI (requires a TTY)"}

	specLevelValues = []string{"project", "org", "account"}
)

func addFlag(f *pflag.FlagSet, spec flagSpec) {
	switch spec.kind {
	case flagKindBool:
		if spec.short != "" {
			f.BoolP(spec.name, spec.short, spec.defBool, spec.usage)
		} else {
			f.Bool(spec.name, spec.defBool, spec.usage)
		}
	case flagKindInt:
		if spec.short != "" {
			f.IntP(spec.name, spec.short, spec.defInt, spec.usage)
		} else {
			f.Int(spec.name, spec.defInt, spec.usage)
		}
	default:
		if spec.short != "" {
			f.StringP(spec.name, spec.short, spec.defStr, spec.usage)
		} else {
			f.String(spec.name, spec.defStr, spec.usage)
		}
	}
}

func addFlags(f *pflag.FlagSet, specs ...flagSpec) {
	for _, s := range specs {
		addFlag(f, s)
	}
}

// CoreFlag describes a flag known to the CLI's core command-building
// machinery: either a root-level flag (pkg/rootcmd, AttachGlobalAuthFlags) or
// a built-in flag wired in per verb/endpoint (bindEndpointCmdFlags,
// bindWorkflowCmd). Per-command custom flags declared in a spec's flags:
// block are NOT core flags — those are enumerated per-command via spec.Flag
// instead, since their names and shapes vary by command.
//
// A core flag's name always has the same shape (bool or value-taking)
// everywhere it's attached, so this single flat table is enough to recognize
// any core flag — and tell whether it consumes a value — anywhere in a raw
// arg list, before the verb or noun is known.
type CoreFlag struct {
	Name  string
	Short string
	Bool  bool // true if the flag takes no value argument
}

var coreFlagTable = []CoreFlag{
	// root (pkg/rootcmd, registry.AttachGlobalAuthFlags)
	{Name: "debug", Bool: true},
	{Name: "timeout"},
	{Name: "profile"},
	{Name: "org"},
	{Name: "project"},
	{Name: "spec", Bool: true},     // plugin binaries only
	{Name: "identity", Bool: true}, // plugin binaries only

	// built-in, wired per verb/endpoint (bindEndpointCmdFlags, bindWorkflowCmd)
	{Name: "format"},
	{Name: "json", Bool: true},
	{Name: "yaml", Bool: true},
	{Name: "columns"},
	{Name: "no-headers", Bool: true},
	{Name: "out", Short: "o"},
	{Name: "raw", Bool: true},
	{Name: "file", Short: "f"},
	{Name: "list-columns", Bool: true},
	{Name: "fields"},
	{Name: "list-fields", Bool: true},
	{Name: "page"},
	{Name: "level"},
	{Name: "offset"},
	{Name: "limit"},
	{Name: "all", Bool: true},
	{Name: "count", Bool: true},
	{Name: "ui", Bool: true},
	{Name: "force", Bool: true},
	{Name: "set"},
	{Name: "del"},
	{Name: "from"},
	{Name: "to"},
}

// coreFlagBool maps every known long ("--name") and short ("-x") spelling of
// a core flag to whether it's a bool flag (no value argument).
var coreFlagBool = buildCoreFlagBool()

func buildCoreFlagBool() map[string]bool {
	m := make(map[string]bool, len(coreFlagTable))
	for _, f := range coreFlagTable {
		m["--"+f.Name] = f.Bool
		if f.Short != "" {
			m["-"+f.Short] = f.Bool
		}
	}
	return m
}

// IndexVerbNoun scans raw CLI args (e.g. os.Args[1:]) and returns the index
// of the verb token and, if present, the noun token, skipping over any core
// flags (see coreFlagTable) that appear before either. verbIdx is -1 if args
// has no positional token at all; nounIdx is -1 if the verb is a leaf
// command or the noun was simply omitted.
//
// This is purely positional — it does not check that the token found is
// actually a registered verb or noun, and it has no knowledge of per-command
// custom flags (flags3), so it can misparse a custom flag placed before the
// noun as the noun itself.
func IndexVerbNoun(args []string) (verbIdx, nounIdx int) {
	verbIdx, next := indexNextPositional(args, 0)
	if verbIdx == -1 {
		return -1, -1
	}
	nounIdx, _ = indexNextPositional(args, next)
	return verbIdx, nounIdx
}

// indexNextPositional returns the index of the next non-flag token at or
// after start, skipping over flags and, where they take one, their value
// argument. next is the index to resume scanning from after the found
// token, or len(args) if none was found.
//
// A flag's own token never carries a value when it embeds one via
// --flag=value. Otherwise: a known core flag (see coreFlagTable) consumes
// the next token iff it isn't a bool flag. An unrecognized flag mirrors
// pflag's actual behavior under FParseErrWhitelist.UnknownFlags (verified
// empirically, since pflag has no declared type to consult for it): it
// consumes the next token as its value unless there isn't one or that token
// itself looks like a flag.
func indexNextPositional(args []string, start int) (idx, next int) {
	for i := start; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			if strings.Contains(a, "=") {
				continue
			}
			if isBool, known := coreFlagBool[a]; known {
				if !isBool {
					i++
				}
				continue
			}
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
			}
			continue
		}
		return i, i + 1
	}
	return -1, len(args)
}

// CanonicalizeNounArg rewrites noun — as it appears in the noun slot: a plain
// noun, "noun:variant", or, for a NounPair verb like migrate, "<from>:<to>" —
// so every alias is replaced by its canonical noun name, using the full
// cross-module alias table (r.nounAliases) built up from every loaded spec.
//
// This exists for plugin dispatch: the host resolves "harness migrate
// scm_bundle:repo" using code's "repo" alias for "repository" and execs the
// owning plugin binary with the original spelling — but that plugin process
// only loads its own module's spec, so it has no "repository" noun to source
// the "repo" alias from and rejects it. Canonicalizing at the boundary, on
// the host side where every alias is known, avoids that mismatch.
//
// The ":" suffix means two different things depending on the verb: for a
// NounPair verb it's a second noun, canonicalized the same as the first
// half; for every other verb it's a variant selector and is left untouched.
func (r *Registry) CanonicalizeNounArg(verb, noun string) string {
	from, rest, hasSuffix := strings.Cut(noun, ":")
	canonFrom := r.canonicalNoun(from)
	if !hasSuffix {
		return canonFrom
	}
	if !verbRegistry[verb].NounPair {
		return canonFrom + ":" + rest
	}
	return canonFrom + ":" + r.canonicalNoun(rest)
}

// canonicalNoun returns noun's canonical name if it's a registered alias,
// otherwise noun unchanged (it may already be canonical, a variant name, or
// simply unknown — all of which pass through as-is).
func (r *Registry) canonicalNoun(noun string) string {
	if canonical, ok := r.nounAliases[noun]; ok {
		return canonical
	}
	return noun
}

// CanonicalizeNounArgs rewrites the noun token in args (if present) via
// CanonicalizeNounArg, locating the verb/noun positions with IndexVerbNoun.
func (r *Registry) CanonicalizeNounArgs(args []string) []string {
	verbIdx, nounIdx := IndexVerbNoun(args)
	if verbIdx == -1 || nounIdx == -1 || nounIdx >= len(args) {
		return args
	}
	canonNoun := r.CanonicalizeNounArg(args[verbIdx], args[nounIdx])
	if canonNoun == args[nounIdx] {
		return args
	}
	out := append([]string(nil), args...)
	out[nounIdx] = canonNoun
	return out
}
