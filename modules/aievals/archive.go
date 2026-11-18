// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// zipEpoch is the ZIP format's earliest timestamp. Every entry uses it so
// file mtimes cannot change the archive bytes.
var zipEpoch = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)

var archiveSuffixes = map[string]bool{
	".yaml":  true,
	".yml":   true,
	".jsonl": true,
}

// BuildArchive zips .harness/evals under repo. Symlinks are omitted. Entry
// names are sorted, slash-separated, and carry no source timestamps.
func BuildArchive(repo string, limits Limits) ([]byte, error) {
	if limits.Members == 0 {
		limits = DefaultLimits()
	}
	root := filepath.Join(repo, ".harness", "evals")
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", root)
	}
	var rels []string
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		lst, lerr := os.Lstat(path)
		if lerr != nil {
			return lerr
		}
		if lst.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		if !lst.Mode().IsRegular() {
			return nil
		}
		if !archiveSuffixes[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		rel, rerr := filepath.Rel(repo, path)
		if rerr != nil {
			return rerr
		}
		rels = append(rels, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(rels) == 0 {
		return nil, fmt.Errorf("archive has no files")
	}
	sort.Strings(rels)
	orderedSizes := make([]int64, len(rels))
	for i, rel := range rels {
		st, serr := os.Lstat(filepath.Join(repo, filepath.FromSlash(rel)))
		if serr != nil {
			return nil, serr
		}
		orderedSizes[i] = st.Size()
	}
	if err := CheckClientLimits(limits, len(rels), orderedSizes, 0); err != nil && !isCompressedLimit(err) {
		return nil, err
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, rel := range rels {
		if err := addStored(zw, repo, rel); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("finalizing zip: %w", err)
	}
	if err := CheckClientLimits(limits, len(rels), orderedSizes, buf.Len()); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func isCompressedLimit(err error) bool {
	return err != nil && strings.Contains(err.Error(), "compressed archive")
}

func addStored(zw *zip.Writer, repo, rel string) error {
	full := filepath.Join(repo, filepath.FromSlash(rel))
	hdr := &zip.FileHeader{
		Name:     rel,
		Method:   zip.Store,
		Modified: zipEpoch,
	}
	hdr.SetMode(0o644)
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		return fmt.Errorf("creating zip entry %s: %w", rel, err)
	}
	f, err := os.Open(full)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(w, f); err != nil {
		return fmt.Errorf("writing zip entry %s: %w", rel, err)
	}
	return nil
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".aievals-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}
