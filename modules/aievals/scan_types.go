// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	"fmt"
	"path/filepath"
	"strings"
)

type Confidence int

const (
	ConfidenceHigh Confidence = iota
	ConfidenceMedium
	ConfidenceLow
)

type FieldEvidence struct {
	Field      string
	Value      string
	Confidence Confidence
	Source     string
}

type Inference struct {
	Language        string
	AgentID         string
	Evidence        []FieldEvidence
	Files           map[string]string
	HasSignal       bool
	Notes           []string
	CandidatePaths  []string
	RouteCandidates []RouteCandidate
}

type RouteCandidate struct {
	Method      string
	Path        string
	Agent       string
	ServiceRoot string
	Adapter     string
	Source      string
}

func (c RouteCandidate) Identity() string {
	return fmt.Sprintf("%s [%s %s]", sanitizeSourcePath(c.Route()), sanitizeSourcePath(c.ServiceRoot), sanitizeSourcePath(c.Source))
}

func (c RouteCandidate) internalIdentity() string {
	return fmt.Sprintf("%s [%s %s]", c.Route(), c.ServiceRoot, c.Source)
}

func (c RouteCandidate) Route() string {
	return canonicalRouteIdentity(c.Method, c.Path)
}

func (inf Inference) ByField(name string) (FieldEvidence, bool) {
	for _, ev := range inf.Evidence {
		if ev.Field == name {
			return ev, true
		}
	}
	return FieldEvidence{}, false
}

func ensureSlash(path string) string {
	if path == "" || strings.HasPrefix(path, "/") {
		if path == "" {
			return "/"
		}
		return path
	}
	return "/" + path
}

func displayName(agent string) string {
	if agent == "" {
		return "App"
	}
	return strings.ToUpper(agent[:1]) + agent[1:]
}

func agentFromDir(repo string) string {
	base := filepath.Base(filepath.Clean(repo))
	if base == "" || base == "." || base == "src" || base == "test" || base == "tests" || base == "main" || base == "app" {
		return "app"
	}
	return slugIdentifier(base)
}

func slugIdentifier(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-' {
			b.WriteRune(char)
			lastDash = char == '-'
		} else if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	if out := strings.Trim(b.String(), "-"); out != "" {
		return out
	}
	return "app"
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, item := range in {
		if item != "" && !seen[item] {
			seen[item] = true
			out = append(out, item)
		}
	}
	return out
}

func canonicalHeader(name string) string {
	switch strings.ToLower(name) {
	case "authorization":
		return "Authorization"
	case "x-api-key":
		return "x-api-key"
	default:
		return name
	}
}

func canonicalRouteIdentity(method, routePath string) string {
	return strings.ToUpper(strings.TrimSpace(method)) + " " + canonicalRoutePath(routePath)
}

func canonicalRoutePath(routePath string) string {
	routePath = strings.TrimSpace(routePath)
	if routePath == "" {
		return "/"
	}
	if !strings.HasPrefix(routePath, "/") {
		routePath = "/" + routePath
	}
	if routePath != "/" {
		routePath = strings.TrimRight(routePath, "/")
	}
	return routePath
}
