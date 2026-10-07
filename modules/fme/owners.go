// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package fme

import (
	"fmt"
	"strings"

	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/spec"
)

// ownersFieldType manages a feature flag's owners collection.
//
// The v4 read shape is [{id, type: USER|GROUP, name, email?}] (email present
// only for USER owners whose account has one resolved). The write shape
// (POST/PATCH) is [{type: USER, id|email}] or [{type: GROUP, identifier}].
//
// For GROUP owners, the read side's "id" field carries the same value the
// write side calls "identifier" (Harness user groups have no separate
// UUID — their identifier is their id). encodeOwners renames it back on the
// way out; it only errors if a GROUP entry has neither.
var ownersFieldType = cmdctx.FieldTypeHandler{
	Normalize: normalizeOwners,
	Mutate:    mutateOwners,
	Encode:    encodeOwners,
}

func normalizeOwners(_ spec.FieldDef, current any) (any, error) {
	items, _ := current.([]any)
	next := make([]any, 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		next = append(next, map[string]any{
			"type":       stringOrEmpty(m["type"]),
			"id":         stringOrEmpty(m["id"]),
			"name":       stringOrEmpty(m["name"]),
			"email":      stringOrEmpty(m["email"]),
			"identifier": stringOrEmpty(m["identifier"]),
		})
	}
	return next, nil
}

func stringOrEmpty(v any) string {
	s, _ := v.(string)
	return s
}

// ownerOperand is a parsed --add/--del owners.<member> operand: either
// user:<email-or-id> or group:<identifier>.
type ownerOperand struct {
	isGroup    bool
	id         string
	email      string
	identifier string
}

func parseOwnerOperand(member string) (ownerOperand, error) {
	scheme, value, found := strings.Cut(member, ":")
	if !found || value == "" {
		return ownerOperand{}, fmt.Errorf("owners: %q must be user:<email-or-id> or group:<identifier>", member)
	}
	switch strings.ToLower(scheme) {
	case "user":
		if strings.Contains(value, "@") {
			return ownerOperand{email: value}, nil
		}
		return ownerOperand{id: value}, nil
	case "group":
		return ownerOperand{isGroup: true, identifier: value}, nil
	default:
		return ownerOperand{}, fmt.Errorf("owners: unrecognized owner scheme %q; use user:<email-or-id> or group:<identifier>", scheme)
	}
}

func ownerMatches(entry map[string]any, op ownerOperand) bool {
	entryType, _ := entry["type"].(string)
	if op.isGroup {
		if !strings.EqualFold(entryType, "GROUP") {
			return false
		}
		identifier, _ := entry["identifier"].(string)
		id, _ := entry["id"].(string)
		name, _ := entry["name"].(string)
		return (identifier != "" && identifier == op.identifier) ||
			(id != "" && id == op.identifier) ||
			(name != "" && name == op.identifier)
	}
	if !strings.EqualFold(entryType, "USER") {
		return false
	}
	id, _ := entry["id"].(string)
	email, _ := entry["email"].(string)
	return (op.id != "" && id == op.id) || (op.email != "" && email == op.email)
}

func mutateOwners(_ spec.FieldDef, current any, op cmdctx.FieldMutation) (any, bool, error) {
	if op.Kind == cmdctx.MutationSet {
		return nil, false, fmt.Errorf("--set %s: use --add/--del to add or remove an individual owner", op.Key)
	}
	if op.Kind != cmdctx.MutationAdd && op.Kind != cmdctx.MutationDelete {
		return nil, false, fmt.Errorf("unknown mutation operation %q", op.Kind)
	}
	selector := op.Key
	if op.Kind == cmdctx.MutationDelete && op.HasValue {
		selector = op.Raw
	}
	_, member, found := strings.Cut(selector, ".")
	if !found || member == "" {
		return nil, false, fmt.Errorf("--%s %s: owners require a member (e.g. --%s owners.user:name@example.com)", op.Kind, op.Key, op.Kind)
	}
	if op.Kind == cmdctx.MutationAdd && op.HasValue {
		return nil, false, fmt.Errorf("--add %s: owner members do not take a value", op.Key)
	}
	owner, err := parseOwnerOperand(member)
	if err != nil {
		return nil, false, err
	}

	items, _ := current.([]any)
	next := make([]any, 0, len(items)+1)
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if op.Kind == cmdctx.MutationDelete && ownerMatches(entry, owner) {
			continue
		}
		next = append(next, entry)
	}
	if op.Kind == cmdctx.MutationAdd {
		exists := false
		for _, item := range next {
			if entry, ok := item.(map[string]any); ok && ownerMatches(entry, owner) {
				exists = true
				break
			}
		}
		if !exists {
			entry := map[string]any{}
			if owner.isGroup {
				entry["type"] = "GROUP"
				entry["identifier"] = owner.identifier
			} else {
				entry["type"] = "USER"
				if owner.id != "" {
					entry["id"] = owner.id
				} else {
					entry["email"] = owner.email
				}
			}
			next = append(next, entry)
		}
	}
	return next, true, nil
}

func encodeOwners(_ spec.FieldDef, current any) (any, error) {
	items, _ := current.([]any)
	encoded := make([]any, 0, len(items))
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		entryType, _ := entry["type"].(string)
		switch strings.ToUpper(entryType) {
		case "USER":
			id, _ := entry["id"].(string)
			email, _ := entry["email"].(string)
			switch {
			case id != "":
				encoded = append(encoded, map[string]any{"type": "USER", "id": id})
			case email != "":
				encoded = append(encoded, map[string]any{"type": "USER", "email": email})
			default:
				return nil, fmt.Errorf("owners: user owner %q has neither id nor email", entry["name"])
			}
		case "GROUP":
			identifier, _ := entry["identifier"].(string)
			if identifier == "" {
				identifier, _ = entry["id"].(string)
			}
			if identifier == "" {
				return nil, fmt.Errorf("owners: group owner %q has no identifier", entry["name"])
			}
			encoded = append(encoded, map[string]any{"type": "GROUP", "identifier": identifier})
		default:
			return nil, fmt.Errorf("owners: unrecognized owner type %q", entryType)
		}
	}
	return encoded, nil
}
