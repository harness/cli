// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package vibe

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func materializeSource(api *vibeAPI, ide ideContext, dest string) error {
	cache := filepath.Join(filepath.Dir(dest), "_git")
	commit := "HEAD"
	if ide.Source.Repository != nil {
		if ide.Source.Repository.Commit != "" {
			commit = ide.Source.Repository.Commit
		} else if ide.Source.Repository.BaseBranch != "" {
			commit = ide.Source.Repository.BaseBranch
		}
	}
	branch := "vibe/" + ide.ExecutionID

	if ide.Source.Kind == "git" && ide.Source.Repository != nil && ide.Source.Repository.URL != "" {
		if err := gitEnsureClone(ide.Source.Repository.URL, cache); err != nil {
			return err
		}
	} else {
		if err := materializeZip(api, ide, cache); err != nil {
			return err
		}
		if err := gitEnsureRepo(cache); err != nil {
			return err
		}
	}
	return gitWorktreeAdd(cache, dest, branch, commit)
}

func materializeZip(api *vibeAPI, ide ideContext, dest string) error {
	if gitRepoExists(dest) {
		fmt.Printf("Reusing source cache %s\n", dest)
		return nil
	}
	fmt.Printf("Downloading source bundle for %s…\n", ide.ApplicationID)
	raw, err := api.getBytes("/api/apps/" + ide.ApplicationID + "/source-bundle")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	return extractZip(raw, dest)
}

func extractZip(raw []byte, dest string) error {
	r, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return fmt.Errorf("reading source zip: %w", err)
	}
	rootPrefix := commonZipRoot(r)
	for _, f := range r.File {
		name := f.Name
		if rootPrefix != "" {
			name = strings.TrimPrefix(name, rootPrefix)
		}
		name = strings.TrimPrefix(name, "/")
		if name == "" || strings.HasPrefix(name, "__MACOSX") || strings.HasSuffix(name, ".DS_Store") {
			continue
		}
		if strings.Contains(name, "..") {
			return fmt.Errorf("refusing zip entry with ..: %s", f.Name)
		}
		target := filepath.Join(dest, filepath.FromSlash(name))
		if !strings.HasPrefix(target, dest+string(os.PathSeparator)) && target != dest {
			return fmt.Errorf("refusing zip slip: %s", f.Name)
		}
		if f.FileInfo().IsDir() || strings.HasSuffix(f.Name, "/") {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			rc.Close()
			return err
		}
		_, copyErr := io.Copy(out, rc)
		closeErr := out.Close()
		rc.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func commonZipRoot(r *zip.Reader) string {
	var prefix string
	for _, f := range r.File {
		name := strings.TrimPrefix(f.Name, "/")
		if name == "" || strings.HasPrefix(name, "__MACOSX") {
			continue
		}
		parts := strings.SplitN(name, "/", 2)
		if len(parts) < 2 {
			return ""
		}
		if prefix == "" {
			prefix = parts[0] + "/"
			continue
		}
		if parts[0]+"/" != prefix {
			return ""
		}
	}
	return prefix
}
