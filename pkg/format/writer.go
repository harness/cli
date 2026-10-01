// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package format

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/harness/cli/v3/pkg/hbase"
)

var outputWriters struct {
	sync.Mutex
	files map[string]*os.File
}

// OpenWriter returns a process-owned writer; callers must not close it.
func OpenWriter(outFile string) (io.Writer, error) {
	if outFile == "" {
		return os.Stdout, nil
	}
	outputWriters.Lock()
	defer outputWriters.Unlock()
	if f := outputWriters.files[outFile]; f != nil {
		return f, nil
	}
	f, err := os.Create(outFile)
	if err != nil {
		return nil, fmt.Errorf("opening output file: %w", err)
	}
	if outputWriters.files == nil {
		outputWriters.files = make(map[string]*os.File)
		hbase.RegisterExitHook(CloseWriters)
	}
	outputWriters.files[outFile] = f
	return f, nil
}

func CloseWriters() error {
	outputWriters.Lock()
	defer outputWriters.Unlock()
	var err error
	for path, f := range outputWriters.files {
		if closeErr := f.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("closing output file %q: %w", path, closeErr))
		}
	}
	outputWriters.files = nil
	return err
}
