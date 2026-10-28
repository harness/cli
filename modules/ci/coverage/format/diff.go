// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package format

import (
	"github.com/harness/cli/v3/modules/ci/coverage/env"
	"github.com/harness/cli/v3/pkg/hlog"
)

// changedLineIndex looks up changed lines by repository-relative file path.
type changedLineIndex struct {
	lines map[string]map[uint32]bool
}

// loadChangedLineIndex returns repository-relative changed lines.
// A missing base is not an error; callers that require a diff for --patch
// check for a nil index. A git failure is returned so --patch can fail closed.
func loadChangedLineIndex(options *writeOptions) (*changedLineIndex, error) {
	if options == nil || options.baseRev == "" {
		return nil, nil
	}

	if options.headRev != "" {
		hlog.Debug("getting diff lines using merge-base range", "base", options.baseRev, "head", options.headRev)
	} else {
		hlog.Debug("getting diff lines for base revision", "base", options.baseRev)
	}

	diffLines, err := env.ChangedLines(options.baseRev, options.headRev)
	if err != nil {
		return nil, err
	}

	hlog.Debug("found files with changes", "count", len(diffLines))
	for filename := range diffLines {
		hlog.Debug("diff file", "file", filename)
	}

	return &changedLineIndex{lines: diffLines}, nil
}

// linesFor returns changed line numbers for filePath, or false if the file is not in the diff.
func (d *changedLineIndex) linesFor(filePath string) (map[uint32]bool, bool) {
	if d == nil || d.lines == nil {
		return nil, false
	}

	fileLines, exists := d.lines[filePath]
	return fileLines, exists
}
