// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package client

import "strings"

// Scope is the Harness account/org/project used on coverage-server requests.
type Scope struct {
	AccountID string
	OrgID     string
	ProjectID string
}

// SpacePath joins the non-empty segments into a Harness space path.
func (s Scope) SpacePath() string {
	segments := make([]string, 0, 3)
	if s.AccountID != "" {
		segments = append(segments, s.AccountID)
	}
	if s.OrgID != "" {
		segments = append(segments, s.OrgID)
	}
	if s.ProjectID != "" {
		segments = append(segments, s.ProjectID)
	}
	return strings.Join(segments, "/")
}

func (s Scope) query() map[string]string {
	params := make(map[string]string)
	if s.OrgID != "" {
		params["orgId"] = s.OrgID
	}
	if s.ProjectID != "" {
		params["projectId"] = s.ProjectID
	}
	return params
}

type EnsureRepoRequest struct {
	Provider string `json:"provider"`
	RepoRef  string `json:"repo_ref"`
}

type EnsureRepoResponse struct {
	Repository *Repository `json:"repository"`
}

type Repository struct {
	ID int64 `json:"id"`
}

type RepoSettings struct {
	ExcludeText *string `json:"exclude_text"`
}
