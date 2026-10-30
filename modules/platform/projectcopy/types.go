// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package projectcopy

import (
	"errors"
	"regexp"
)

var identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
var accountIdentifier = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func ValidIdentifier(s string) bool { return identifier.MatchString(s) }

type Scope struct {
	Account     string `json:"account"`
	Org         string `json:"org"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
}

func (s Scope) Validate() error {
	if !accountIdentifier.MatchString(s.Account) || !ValidIdentifier(s.Org) || !ValidIdentifier(s.Source) || !ValidIdentifier(s.Destination) || s.Source == s.Destination {
		return errors.New("project planning requires an account, org, and distinct explicit source/destination project identifiers")
	}
	return nil
}

type Capability string

const (
	Copyable        Capability = "copyable"
	ReferenceOnly   Capability = "reference-only"
	MappingRequired Capability = "mapping-required"
	Blocked         Capability = "blocked"
)

type Item struct {
	Number     int        `json:"number"`
	Type       string     `json:"type"`
	Capability Capability `json:"capability"`
	Reason     string     `json:"reason"`
}

type Action string

const (
	ActionCreate   Action = "create"
	ActionSkip     Action = "skip"
	ActionConflict Action = "conflict"
	ActionBlocked  Action = "blocked"
	ActionShared   Action = "shared"
	ActionSkipped  Action = "skipped"
)

// InlineSecretPlaceholder is the shell value stored for copied inline SecretText.
// The source value is never read or written. Replace it in the destination before running anything.
const InlineSecretPlaceholder = "123456"

type Result struct {
	Kind       string `json:"kind"`
	Identifier string `json:"identifier"`
	Action     Action `json:"action"`
	Reason     string `json:"reason,omitempty"`
}

type Report struct {
	Scope               Scope    `json:"scope"`
	Coverage            string   `json:"coverage"`
	Mode                string   `json:"mode"`
	InventoryComplete   bool     `json:"inventory_complete"`
	ApplyAvailable      bool     `json:"apply_available"`
	Readiness           string   `json:"readiness"`
	NotInventoried      []string `json:"not_inventoried"`
	Secrets             []Item   `json:"secrets"`
	Results             []Result `json:"results,omitempty"`
	DelegateSelectors   []Result `json:"delegate_selectors,omitempty"`
	SecretsPendingValue []string `json:"secrets_pending_value,omitempty"`
}

func NewReport(s Scope) Report {
	return Report{Scope: s, Coverage: "PARTIAL", Mode: "read-only planning", Readiness: "NOT ASSESSED; destination not checked or created", NotInventoried: []string{"connectors", "pipelines", "all other project resources"}, Secrets: []Item{}}
}

func NewCopyReport(s Scope, apply bool) Report {
	mode, readiness := "plan", "plan only; pass --apply to copy"
	if apply {
		mode, readiness = "apply", "copying"
	}
	return Report{
		Scope: s, Coverage: "cd-core", Mode: mode, ApplyAvailable: true, Readiness: readiness,
		NotInventoried: []string{"code repositories", "gitops", "feature flags", "executions", "delegate installations"},
		Secrets:        []Item{},
	}
}
