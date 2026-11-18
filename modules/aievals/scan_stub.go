// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

// inferSupportedAdapters is completed by the language-adapter follow-up.
// Keeping the contract boundary here lets the core PR land independently.
func inferSupportedAdapters(repo, selection string) (Inference, error) {
	return Inference{Language: "adapter"}, nil
}

func Infer(repo string) (Inference, error) {
	return inferSupportedAdapters(repo, "")
}

func InferWithRoute(repo, route string) (Inference, error) {
	return inferSupportedAdapters(repo, route)
}

func selectAdapterRoute(candidates []RouteCandidate, selection string) (RouteCandidate, bool) {
	return RouteCandidate{}, false
}
