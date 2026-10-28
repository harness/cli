// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func condaTar(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, body := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeCondaZip(t *testing.T, zipEntries map[string][]byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pkg-1.0-0.conda")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, data := range zipEntries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func zstdCompress(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := zw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestCondaMetaFromConda(t *testing.T) {
	const index = `{"name":"foo","version":"1.2.3","subdir":"linux-64"}`
	infoTar := zstdCompress(t, condaTar(t, map[string]string{"info/index.json": index}))
	noIndexTar := zstdCompress(t, condaTar(t, map[string]string{"info/about.json": "{}"}))

	tests := []struct {
		name    string
		path    string
		want    condaIndexJSON
		wantErr bool
	}{
		{
			name: "valid",
			path: writeCondaZip(t, map[string][]byte{
				"pkg-foo-1.2.3-0.tar.zst":  zstdCompress(t, condaTar(t, map[string]string{"lib/x": "x"})),
				"info-foo-1.2.3-0.tar.zst": infoTar,
			}),
			want: condaIndexJSON{Name: "foo", Version: "1.2.3", Subdir: "linux-64"},
		},
		{
			name:    "no info entry",
			path:    writeCondaZip(t, map[string][]byte{"pkg-foo-1.2.3-0.tar.zst": infoTar}),
			wantErr: true,
		},
		{
			name:    "info tar without index.json",
			path:    writeCondaZip(t, map[string][]byte{"info-foo-1.2.3-0.tar.zst": noIndexTar}),
			wantErr: true,
		},
		{
			name: "not a zip",
			path: func() string {
				p := filepath.Join(t.TempDir(), "bad.conda")
				if err := os.WriteFile(p, []byte("not a zip"), 0o644); err != nil {
					t.Fatal(err)
				}
				return p
			}(),
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := condaMetaFromConda(tt.path)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && *got != tt.want {
				t.Errorf("got %+v, want %+v", *got, tt.want)
			}
		})
	}
}
