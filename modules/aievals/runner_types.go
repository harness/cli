// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

// RunnerLayout describes the pinned runner executables used by the workflow
// integration. Candidate writes keep the verifier injectable through this type.
type RunnerLayout struct {
	Home    string
	Python  string
	AIEvals string
}
