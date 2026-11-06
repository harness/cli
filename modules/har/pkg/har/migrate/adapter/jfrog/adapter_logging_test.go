package jfrog

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/harness/cli/modules/har/pkg/har/migrate/types"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func TestSampleStrings(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		n    int
		want []string
	}{
		{name: "shorter than limit returns all", in: []string{"a", "b"}, n: 20, want: []string{"a", "b"}},
		{name: "longer than limit is truncated", in: []string{"a", "b", "c"}, n: 2, want: []string{"a", "b"}},
		{name: "empty returns empty", in: []string{}, n: 20, want: []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sampleStrings(tt.in, tt.n)
			if len(got) != len(tt.want) {
				t.Fatalf("sampleStrings(%v, %d) len = %d, want %d", tt.in, tt.n, len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("sampleStrings(%v, %d)[%d] = %q, want %q", tt.in, tt.n, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestFileURIs(t *testing.T) {
	files := makeSizedFiles(logSampleLimit + 5)

	got := fileURIs(files, logSampleLimit)
	if len(got) != logSampleLimit {
		t.Fatalf("fileURIs len = %d, want %d", len(got), logSampleLimit)
	}
	if got[logSampleLimit-1] != "dir/file-19.bin" {
		t.Errorf("fileURIs last sample = %q, want %q", got[logSampleLimit-1], "dir/file-19.bin")
	}

	if got := fileURIs(files[:3], logSampleLimit); len(got) != 3 {
		t.Errorf("fileURIs with fewer files than limit len = %d, want 3", len(got))
	}
}

func TestSearchedFilePaths(t *testing.T) {
	files := []types.SearchedFile{
		{Path: "com/acme", Name: "a.jar"},
		{Path: ".", Name: "root.bin"},
	}
	got := searchedFilePaths(files, logSampleLimit)
	if len(got) != 2 {
		t.Fatalf("searchedFilePaths len = %d, want 2", len(got))
	}
	if got[0] != "com/acme/a.jar" {
		t.Errorf("searchedFilePaths[0] = %q, want %q", got[0], "com/acme/a.jar")
	}
	if got[1] != "root.bin" {
		t.Errorf("searchedFilePaths[1] = %q, want %q", got[1], "root.bin")
	}
}

type stubFilesClient struct {
	Client
	files []types.File
}

func (s stubFilesClient) GetFiles(string) ([]types.File, error) { return s.files, nil }

func makeSizedFiles(n int) []types.File {
	files := make([]types.File, 0, n)
	for i := 0; i < n; i++ {
		files = append(files, types.File{Uri: fmt.Sprintf("dir/file-%02d.bin", i), Size: i})
	}
	return files
}

func captureLog(t *testing.T, level zerolog.Level) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := log.Logger
	log.Logger = zerolog.New(buf).Level(level)
	t.Cleanup(func() { log.Logger = prev })
	return buf
}

func TestGetFilesLogIsBoundedAtEveryLevel(t *testing.T) {
	for _, level := range []zerolog.Level{zerolog.InfoLevel, zerolog.DebugLevel, zerolog.TraceLevel} {
		t.Run(level.String(), func(t *testing.T) {
			buf := captureLog(t, level)
			a := NewAdapterWithClient(types.RegistryConfig{}, stubFilesClient{files: makeSizedFiles(logSampleLimit + 5)})

			if _, err := a.GetFiles("generic-local"); err != nil {
				t.Fatalf("GetFiles: %v", err)
			}

			out := buf.String()
			if !strings.Contains(out, "25 file(s)") {
				t.Errorf("log missing total count: %q", out)
			}
			if !strings.Contains(out, "first 20") {
				t.Errorf("log missing sample bound: %q", out)
			}
			for _, leaked := range []string{"file-20", "file-21", "file-22", "file-23", "file-24"} {
				if strings.Contains(out, leaked) {
					t.Errorf("log leaked entry past sample bound: %q", leaked)
				}
			}
			if lines := strings.Count(strings.TrimSpace(out), "\n"); lines != 0 {
				t.Errorf("expected a single log line, got %d lines", lines+1)
			}
		})
	}
}
