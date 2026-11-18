// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// FormatSkillModule renders the canonical skill as a TypeScript module.
// The header names the source path and the sha256 of the skill bytes.
// Hand-editing the module without updating the hash fails the UI drift test.
func FormatSkillModule(sourceLabel, body string, skillPath ...string) string {
	sum := sha256.Sum256([]byte(body))
	hash := hex.EncodeToString(sum[:])
	safeSource := sanitizeGeneratedLabel(sourceLabel)
	safeSkill := safeSource
	if len(skillPath) > 0 && skillPath[0] != "" {
		safeSkill = sanitizeGeneratedLabel(skillPath[0])
	}
	regenerate := fmt.Sprintf(
		"go run ./modules/aievals/cmd/skillgen -skill %s -source-label %s -out <this-file>",
		shellQuote(safeSkill),
		shellQuote(safeSource),
	)
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(body); err != nil {
		encoded.Reset()
		fallback, _ := json.Marshal(body)
		encoded.Write(fallback)
	}
	literal := bytes.TrimRight(encoded.Bytes(), "\n")
	return fmt.Sprintf(`// Generated from harness-cli %s
// source: %s
// sha256: %s
// Do not edit by hand. Regenerate with: %s

export const AI_EVALS_SKILL_SOURCE = %q
export const AI_EVALS_SKILL_SHA256 = %q
export const AI_EVALS_SKILL_MD: string = %s
`, safeSource, safeSource, hash, regenerate, safeSource, hash, literal)
}

func sanitizeGeneratedLabel(value string) string {
	return strings.Map(func(char rune) rune {
		switch char {
		case '\r', '\n', '\t':
			return ' '
		default:
			return char
		}
	}, value)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
