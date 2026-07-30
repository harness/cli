// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/harness/cli/pkg/auth"
)

// atomicWrite writes content to path via a temp file + rename, ensuring an
// incomplete write never leaves a partial file. perm is applied before rename.
func atomicWrite(path string, content []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".harness-tmp-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Chmod(tmpPath, perm); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("setting permissions: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("renaming temp file: %w", err)
	}
	return nil
}

// parseRegistryAndName splits ctx.Id ("registry/name") into its two parts.
func parseRegistryAndName(id string) (registry, name string, err error) {
	parts := strings.SplitN(id, "/", 3)
	if len(parts) < 2 || parts[1] == "" {
		return "", "", fmt.Errorf("artifact id must be <registry>/<name>, got %q", id)
	}
	if len(parts) == 3 {
		return "", "", fmt.Errorf("artifact name must not contain '/': use --version for the version, got %q", id)
	}
	return parts[0], parts[1], nil
}

// newHTTPClient returns an HTTP client with a 10-minute timeout suitable for large artifact uploads/downloads.
func newHTTPClient() *http.Client {
	return &http.Client{Timeout: 10 * time.Minute}
}

// setAuthHeader sets the appropriate auth header on req (Bearer for SSO, x-api-key for PAT).
func setAuthHeader(req *http.Request, a *auth.ResolvedAuth) {
	a.SetAuthHeader(req)
}

// doRequest executes req, reads the response body, and returns an error for non-2xx responses.
// On success it returns the body bytes (may be empty).
func doRequest(c *http.Client, req *http.Request) ([]byte, error) {
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

// buildPkgURL constructs a registry URL of the form:
//
//	{registryURL}/pkg/{accountID}/{subpath}?accountIdentifier={accountID}
//
// subpath is everything after /pkg/{accountID}/, e.g. "my-registry/files/myapp/1.0/app.jar".
// Slashes in subpath are preserved.
func buildPkgURL(registryURL, accountID, subpath string) (string, error) {
	base, err := url.Parse(registryURL)
	if err != nil {
		return "", fmt.Errorf("invalid registry URL %q: %w", registryURL, err)
	}
	base.Path = fmt.Sprintf("/pkg/%s/%s", url.PathEscape(accountID), subpath)
	q := base.Query()
	q.Set("accountIdentifier", accountID)
	base.RawQuery = q.Encode()
	return base.String(), nil
}

// savePkgmgrConfig writes a pkgmgrSavedConfig to ~/.harness/<name>-pkgmgr.json.
func savePkgmgrConfig(name string, cfg pkgmgrSavedConfig) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, ".harness")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(dir, name+"-pkgmgr.json"), data, 0600)
}

// loadPkgmgrConfig reads ~/.harness/<name>-pkgmgr.json; returns nil if absent.
func loadPkgmgrConfig(name string) *pkgmgrSavedConfig {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(home, ".harness", name+"-pkgmgr.json"))
	if err != nil {
		return nil
	}
	var cfg pkgmgrSavedConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil
	}
	return &cfg
}

// readFileFromTarGz reads the contents of the first file in archivePath whose path
// ends with targetSuffix (case-sensitive). Returns an error if not found.
func readFileFromTarGz(archivePath, targetSuffix string) ([]byte, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return nil, fmt.Errorf("opening %q: %w", archivePath, err)
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("opening gzip %q: %w", archivePath, err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading tar %q: %w", archivePath, err)
		}
		if strings.HasSuffix(hdr.Name, targetSuffix) {
			return io.ReadAll(tr)
		}
	}
	return nil, fmt.Errorf("%q not found in %q", targetSuffix, archivePath)
}

// readFileFromZip reads the contents of the first entry in archivePath whose name
// ends with targetSuffix. Returns an error if not found.
func readFileFromZip(archivePath, targetSuffix string) ([]byte, error) {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, fmt.Errorf("opening zip %q: %w", archivePath, err)
	}
	defer zr.Close()

	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, targetSuffix) {
			rc, err := f.Open()
			if err != nil {
				return nil, fmt.Errorf("opening %q in zip: %w", f.Name, err)
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("%q not found in %q", targetSuffix, archivePath)
}
