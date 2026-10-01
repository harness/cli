// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package format

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/hbase"
	"github.com/harness/cli/v3/pkg/spec"
)

func TestOutputBatchesShareWriter(t *testing.T) {
	for _, name := range []string{"table", "jsonl"} {
		t.Run(name, func(t *testing.T) {
			path, read := outFile(t)
			if err := os.WriteFile(path, []byte("old contents"), 0600); err != nil {
				t.Fatal(err)
			}
			w, err := OpenWriter(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.WriteString(w, "summary\n"); err != nil {
				t.Fatal(err)
			}
			flags := cmdctx.FormatFlags{Format: name, OutFile: path}
			tspec := &spec.TableSpec{Columns: []spec.TableColumn{{Header: "ID", Expr: "it.id"}}}
			for _, id := range []string{"first-batch", "second-batch"} {
				data := []any{map[string]any{"id": id}}
				if err := FormatArrayOutput(flags, false, data, "it", tspec, nil, map[string]any{}, nil); err != nil {
					t.Fatal(err)
				}
			}
			got := string(read())
			for _, want := range []string{"summary", "first-batch", "second-batch"} {
				if !strings.Contains(got, want) {
					t.Errorf("output %q missing %q", got, want)
				}
			}
			if strings.Contains(got, "old contents") {
				t.Errorf("first open did not truncate: %q", got)
			}
			if err := hbase.Cleanup(); err != nil {
				t.Fatal(err)
			}
			if _, err := io.WriteString(w, "closed"); !errors.Is(err, os.ErrClosed) {
				t.Fatalf("write after cleanup: %v", err)
			}
			w, err = OpenWriter(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.WriteString(w, "new invocation"); err != nil {
				t.Fatal(err)
			}
			if got := string(read()); got != "new invocation" {
				t.Errorf("reopened output = %q", got)
			}
		})
	}
}

func TestWriterFailuresAndStdout(t *testing.T) {
	path, read := outFile(t)
	if err := os.WriteFile(path, []byte("preserved"), 0600); err != nil {
		t.Fatal(err)
	}
	flags := cmdctx.FormatFlags{Format: "invalid", OutFile: path}
	if err := FormatArrayOutput(flags, false, nil, "it", nil, nil, nil, nil); err == nil {
		t.Fatal("expected format validation error")
	}
	if got := string(read()); got != "preserved" {
		t.Fatalf("validation truncated output: %q", got)
	}
	w, err := OpenWriter("")
	if err != nil || w != os.Stdout {
		t.Fatalf("stdout writer = %v, error = %v", w, err)
	}
	if err := hbase.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stdout.Stat(); err != nil {
		t.Fatalf("cleanup closed stdout: %v", err)
	}
	missing := filepath.Join(t.TempDir(), "missing")
	path = filepath.Join(missing, "out")
	if _, err := OpenWriter(path); err == nil {
		t.Fatal("expected open failure")
	}
	if err := os.Mkdir(missing, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenWriter(path); err != nil {
		t.Fatalf("retry after open failure: %v", err)
	}
}

func TestConcurrentOpenWriter(t *testing.T) {
	path, _ := outFile(t)
	writers := make(chan io.Writer, 8)
	var wg sync.WaitGroup
	for range cap(writers) {
		wg.Go(func() {
			w, err := OpenWriter(path)
			if err != nil {
				t.Error(err)
			}
			writers <- w
		})
	}
	wg.Wait()
	close(writers)
	first := <-writers
	for w := range writers {
		if w != first {
			t.Fatal("concurrent opens returned different writers")
		}
	}
}

func TestCloseWritersResetsAfterFailure(t *testing.T) {
	path, _ := outFile(t)
	w, err := OpenWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	otherPath, _ := outFile(t)
	other, err := OpenWriter(otherPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.(*os.File).Close(); err != nil {
		t.Fatal(err)
	}
	if err := CloseWriters(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("cleanup error = %v, want closed-file error", err)
	}
	if _, err := io.WriteString(other, "closed"); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("cleanup skipped another writer: %v", err)
	}
	if err := CloseWriters(); err != nil {
		t.Fatalf("repeated cleanup: %v", err)
	}
	if _, err := OpenWriter(path); err != nil {
		t.Fatalf("reopen after cleanup failure: %v", err)
	}
}
