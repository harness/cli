// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package ci

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// envArtifactFilePath is the pipeline-injected path `execute artifact:upload`
// writes its JSON summary to. Unset means the step doesn't want the summary.
const envArtifactFilePath = "HARNESS_ARTIFACT_FILE_PATH"

// writeArtifactOutput merges the current push results into the artifact output
// file named by $HARNESS_ARTIFACT_FILE_PATH. If the file already exists (a
// prior upload in the same Run step wrote it), entries are merged by
// destination path — the latest upload for a given destination wins, so
// re-uploads always reflect the newest version.
//
// An exclusive advisory lock on a companion .lock file serializes concurrent
// `execute artifact:upload` processes (e.g. parallel matrix jobs in the same
// step) so the read-merge-write cycle is atomic.
func writeArtifactOutput(output artifactOutput) error {
	outPath := os.Getenv(envArtifactFilePath)
	if outPath == "" {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}

	lockPath := outPath + ".lock"
	lf, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("open artifact lock file: %w", err)
	}
	defer lf.Close()

	if err := lockFile(lf.Fd()); err != nil {
		return fmt.Errorf("acquire artifact lock: %w", err)
	}
	defer unlockFile(lf.Fd()) //nolint:errcheck

	if existing, err := readArtifactOutput(outPath); err == nil {
		output = mergeArtifactOutputs(existing, output)
	}

	data, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(outPath, data, 0o644)
}

// mergeArtifactOutputs merges old and new outputs, deduplicating by
// destination path. The newer entry always replaces the older one so that
// re-uploads are reflected correctly. Totals are recomputed from the merged
// file list.
func mergeArtifactOutputs(old, new artifactOutput) artifactOutput { //nolint:predeclared
	// Index old entries by destination; newer entries overwrite them.
	byDest := make(map[string]pushedFile, len(old.Files))
	for _, f := range old.Files {
		byDest[f.Destination] = f
	}
	for _, f := range new.Files {
		byDest[f.Destination] = f
	}

	// Preserve a stable order: old entries first (in original order), then
	// any new destinations appended in the order they were uploaded.
	seen := make(map[string]bool, len(byDest))
	var merged []pushedFile
	for _, f := range old.Files {
		merged = append(merged, byDest[f.Destination])
		seen[f.Destination] = true
	}
	for _, f := range new.Files {
		if !seen[f.Destination] {
			merged = append(merged, f)
		}
	}

	var totalBytes int64
	for _, f := range merged {
		totalBytes += f.Size
	}

	// Prefer the latest non-empty UUID values.
	pkgUUID := new.HarPackageUUID
	if pkgUUID == "" {
		pkgUUID = old.HarPackageUUID
	}
	verUUID := new.HarVersionUUID
	if verUUID == "" {
		verUUID = old.HarVersionUUID
	}

	return artifactOutput{
		ArtifactName:   new.ArtifactName,
		Version:        new.Version,
		Registry:       new.Registry,
		HarPackageUUID: pkgUUID,
		HarVersionUUID: verUUID,
		FileCount:      len(merged),
		TotalBytes:     totalBytes,
		Files:          merged,
	}
}

func readArtifactOutput(path string) (artifactOutput, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return artifactOutput{}, err
	}
	var out artifactOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return artifactOutput{}, err
	}
	return out, nil
}
