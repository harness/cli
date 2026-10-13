// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package ci

// artifactOutput is written (and merged across multiple invocations within
// the same Run step) to $HARNESS_ARTIFACT_FILE_PATH so CI Manager sees a
// single unified record of everything `execute artifact:upload` pushed.
type artifactOutput struct {
	ArtifactName   string       `json:"artifactName"`
	Version        string       `json:"version"`
	Registry       string       `json:"registry"`
	HarPackageUUID string       `json:"harPackageUuid,omitempty"`
	HarVersionUUID string       `json:"harVersionUuid,omitempty"`
	FileCount      int          `json:"fileCount"`
	TotalBytes     int64        `json:"totalBytes"`
	Files          []pushedFile `json:"files"`
}

type pushedFile struct {
	Name        string `json:"name"`
	SourcePath  string `json:"sourcePath"`
	Destination string `json:"destination"`
	Size        int64  `json:"size"`
	UploadedAt  string `json:"uploadedAt"`
	SHA256      string `json:"sha256,omitempty"`
}
