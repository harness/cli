// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import "fmt"

// Archive limits shared with the D3 import route.
// TODO(onboarding-archive-limits): provisional until the security owner ratifies them.
const (
	MaxMembers          = 256
	MaxMemberBytes      = 10 * 1024 * 1024
	MaxCompressedBytes  = 10 * 1024 * 1024
	MaxExpandedBytes    = 50 * 1024 * 1024
	MaxCompressionRatio = 100
)

// Limits is the subset the CLI enforces. Expanded bytes and the compression
// ratio stay authoritative on the backend.
type Limits struct {
	Members     int
	MemberBytes int64
	Compressed  int64
}

func DefaultLimits() Limits {
	return Limits{
		Members:     MaxMembers,
		MemberBytes: MaxMemberBytes,
		Compressed:  MaxCompressedBytes,
	}
}

// CheckClientLimits enforces member count, per-file size, and compressed size.
// expanded is accepted at any size, including past MaxExpandedBytes, and a
// compressed payload far below expanded does not trip MaxCompressionRatio.
func CheckClientLimits(limits Limits, members int, sizes []int64, compressed int) error {
	if limits.Members <= 0 {
		limits = DefaultLimits()
	}
	if members > limits.Members {
		return fmt.Errorf("archive exceeds the provisional file count (%d)", limits.Members)
	}
	for _, size := range sizes {
		if size > limits.MemberBytes {
			return fmt.Errorf("archive member exceeds the provisional per-file limit (%d bytes)", limits.MemberBytes)
		}
	}
	if int64(compressed) > limits.Compressed {
		return fmt.Errorf("compressed archive exceeds the provisional limit (%d bytes)", limits.Compressed)
	}
	return nil
}
