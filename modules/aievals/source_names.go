// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import "strings"

// These markers are shared by the language adapters when describing request
// parameters. The Java adapter owns the annotation table; the core adapters
// reuse the same parameter-shape vocabulary.
type javaAnnotationKind int

const (
	javaQueryParam javaAnnotationKind = iota + 1
	javaPathParam
	javaHeaderParam
	javaBodyParam
	javaDefaultValue
)

func compactJavaName(name string) string {
	return strings.Join(strings.Fields(name), "")
}

// chooseRouteHeader keeps a declared auth header, or a lone declared header.
func chooseRouteHeader(names []string) string {
	names = uniqueStrings(names)
	for _, name := range names {
		switch canonical := canonicalHeader(name); canonical {
		case "Authorization", "x-api-key":
			return canonical
		}
	}
	if len(names) == 1 {
		return canonicalHeader(names[0])
	}
	return ""
}
