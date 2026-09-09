// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"fmt"
	"runtime"

	"github.com/harness/cli/v3/pkg/hbase"
)

// UserAgentString returns the User-Agent value sent with all outgoing migrate
// HTTP requests, e.g. "harness-cli/3.1.2 (darwin/arm64)".
func UserAgentString() string {
	return fmt.Sprintf("harness-cli/%s (%s/%s)", hbase.Version, runtime.GOOS, runtime.GOARCH)
}
