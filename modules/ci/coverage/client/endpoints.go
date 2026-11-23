// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package client

import "fmt"

const APIPrefix = "/coverage/api/v1"

func EnsureRepoPath(accountID string) string {
	return fmt.Sprintf(APIPrefix+"/accounts/%s/repos", accountID)
}

func RepoSettingsPath(accountID string, repoID int64) string {
	return fmt.Sprintf(APIPrefix+"/accounts/%s/repos/%d/settings", accountID, repoID)
}
