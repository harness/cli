
// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"crypto/md5" //nolint:gosec // Conan revision is MD5 by spec, not for security.
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/harness/cli/pkg/cmdctx"
)

// Conan file-name constants (mirrors the conanutil package in the legacy CLI).
const (
	conanPlaceholder  = "_"
	conanManifestFile = "conanmanifest.txt"
	conanFilePy       = "conanfile.py"
	conanInfoTxt      = "conaninfo.txt"
	conanTarballExport  = "conan_export"
	conanTarballSources = "conan_sources"
	conanTarballPackage = "conan_package"
)

var (
	conanTarballExtensions = map[string]bool{".tgz": true, ".txz": true, ".tzst": true}
	conanNamePattern       = regexp.MustCompile(`^[a-z0-9_][a-z0-9_+.\-]{1,100}$`)
	conanRevisionPattern   = regexp.MustCompile(`^([a-f0-9]{32}|[a-f0-9]{40})$`)
	conanPackageIDPattern  = regexp.MustCompile(`^[a-f0-9]{40}$`)
)

type conanRef struct {
	Name    string
	Version string
	User    string
	Channel string
}

func (r conanRef) display() string {
	if r.User == conanPlaceholder && r.Channel == conanPlaceholder {
		return r.Name + "/" + r.Version
	}
	return r.Name + "/" + r.Version + "@" + r.User + "/" + r.Channel
}

// pushConanArtifact implements "push artifact:conan".
//
// Usage:
//
//	harness push artifact:conan <registry> <name/version[@user/channel]> <recipe-dir>
//	  [--package-dir <dir> --package-id <sha>]
//	  [--recipe-revision <md5>] [--package-revision <md5>]
func pushConanArtifact(ctx *cmdctx.Ctx) error {
	if len(ctx.Args) < 2 {
		return fmt.Errorf("push conan artifact requires: push artifact <registry> <reference> <recipe-dir>")
	}

	registry := ctx.Id
	reference := ctx.Args[0]
	recipeDir := ctx.Args[1]

	ref, err := parseConanRef(reference)
	if err != nil {
		return fmt.Errorf("invalid Conan reference: %w", err)
	}

	recipeRevision := cmdctx.GetString(ctx.FlagValues, "recipe-revision")
	packageDir := cmdctx.GetString(ctx.FlagValues, "package-dir")
	packageID := cmdctx.GetString(ctx.FlagValues, "package-id")
	packageRevision := cmdctx.GetString(ctx.FlagValues, "package-revision")

	// Collect recipe files
	recipeFiles, skipped, err := collectConanFiles(recipeDir, isConanRecipeFile)
	if err != nil {
		return fmt.Errorf("reading recipe dir: %w", err)
	}
	if len(skipped) > 0 {
		fmt.Fprintf(os.Stderr, "Skipping %d non-Conan file(s): %s\n", len(skipped), strings.Join(skipped, ", "))
	}

	// Derive RREV from manifest if not provided
	if recipeRevision == "" {
		recipeRevision, err = conanRevisionFromManifest(recipeDir)
		if err != nil {
			return fmt.Errorf("deriving recipe revision: %w", err)
		}
	}
	if !conanRevisionPattern.MatchString(recipeRevision) {
		return fmt.Errorf("recipe-revision must be a 32-char MD5 or 40-char SHA, got: %q", recipeRevision)
	}

	// Validate package-layer inputs before uploading anything
	var packageFiles []string
	if packageDir != "" {
		if packageID == "" {
			return fmt.Errorf("--package-id is required when --package-dir is set")
		}
		if !conanPackageIDPattern.MatchString(packageID) {
			return fmt.Errorf("package-id must be a 40-char SHA-1, got: %q", packageID)
		}
		var pkgSkipped []string
		packageFiles, pkgSkipped, err = collectConanFiles(packageDir, isConanPackageFile)
		if err != nil {
			return fmt.Errorf("reading package dir: %w", err)
		}
		if len(pkgSkipped) > 0 {
			fmt.Fprintf(os.Stderr, "Skipping %d non-Conan package file(s): %s\n", len(pkgSkipped), strings.Join(pkgSkipped, ", "))
		}
		if packageRevision == "" {
			packageRevision, err = conanRevisionFromManifest(packageDir)
			if err != nil {
				return fmt.Errorf("deriving package revision: %w", err)
			}
		}
		if !conanRevisionPattern.MatchString(packageRevision) {
			return fmt.Errorf("package-revision must be a 32-char MD5 or 40-char SHA, got: %q", packageRevision)
		}
	}

	client := newHTTPClient()

	// Upload recipe layer (manifest last)
	fmt.Fprintf(os.Stderr, "Uploading recipe files (rrev %s) ...\n", recipeRevision)
	for _, fp := range orderConanFiles(recipeFiles) {
		if err := uploadConanRecipe(ctx, client, registry, ref, recipeRevision, fp); err != nil {
			return err
		}
	}

	// Upload package layer if requested (manifest last)
	if packageDir != "" {
		fmt.Fprintf(os.Stderr, "Uploading package files (pkgid %s, prev %s) ...\n", packageID, packageRevision)
		for _, fp := range orderConanFiles(packageFiles) {
			if err := uploadConanPackage(ctx, client, registry, ref, recipeRevision, packageID, packageRevision, fp); err != nil {
				return err
			}
		}
	}

	fmt.Fprintf(os.Stderr, "Successfully pushed Conan package %s to registry %q\n", ref.display(), registry)
	return nil
}

// uploadConanRecipe PUTs a single recipe-layer file.
// URL: /pkg/{account}/{registry}/conan/v2/conans/{name}/{version}/{user}/{channel}/revisions/{rrev}/files/{filename}
func uploadConanRecipe(ctx *cmdctx.Ctx, client *http.Client, registry string, ref conanRef, rrev, filePath string) error {
	fileName := filepath.Base(filePath)
	fmt.Fprintf(os.Stderr, "  Uploading recipe file: %s\n", fileName)

	checksums, err := computeFileChecksums(filePath)
	if err != nil {
		return fmt.Errorf("computing checksums for %s: %w", fileName, err)
	}

	uploadURL := fmt.Sprintf("%s/pkg/%s/%s/conan/v2/conans/%s/%s/%s/%s/revisions/%s/files/%s",
		strings.TrimRight(ctx.Auth.RegistryURL, "/"),
		url.PathEscape(ctx.Auth.AccountID),
		url.PathEscape(registry),
		url.PathEscape(ref.Name),
		url.PathEscape(ref.Version),
		url.PathEscape(ref.User),
		url.PathEscape(ref.Channel),
		url.PathEscape(rrev),
		url.PathEscape(fileName),
	)

	return conanPutFile(ctx, client, uploadURL, filePath, checksums)
}

// uploadConanPackage PUTs a single package-layer file.
// URL: /pkg/{account}/{registry}/conan/v2/conans/{name}/{version}/{user}/{channel}/revisions/{rrev}/packages/{pkgid}/revisions/{prev}/files/{filename}
func uploadConanPackage(ctx *cmdctx.Ctx, client *http.Client, registry string, ref conanRef, rrev, pkgID, prev, filePath string) error {
	fileName := filepath.Base(filePath)
	fmt.Fprintf(os.Stderr, "  Uploading package file: %s\n", fileName)

	checksums, err := computeFileChecksums(filePath)
	if err != nil {
		return fmt.Errorf("computing checksums for %s: %w", fileName, err)
	}

	uploadURL := fmt.Sprintf("%s/pkg/%s/%s/conan/v2/conans/%s/%s/%s/%s/revisions/%s/packages/%s/revisions/%s/files/%s",
		strings.TrimRight(ctx.Auth.RegistryURL, "/"),
		url.PathEscape(ctx.Auth.AccountID),
		url.PathEscape(registry),
		url.PathEscape(ref.Name),
		url.PathEscape(ref.Version),
		url.PathEscape(ref.User),
		url.PathEscape(ref.Channel),
		url.PathEscape(rrev),
		url.PathEscape(pkgID),
		url.PathEscape(prev),
		url.PathEscape(fileName),
	)

	return conanPutFile(ctx, client, uploadURL, filePath, checksums)
}

func conanPutFile(ctx *cmdctx.Ctx, client *http.Client, uploadURL, filePath string, checksums fileChecksums) error {
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("opening %q: %w", filePath, err)
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat %q: %w", filePath, err)
	}

	req, err := http.NewRequest("PUT", uploadURL, f)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	setAuthHeader(req, ctx.Auth)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = fi.Size()
	req.Header.Set("X-Checksum-Sha1", checksums.SHA1)

	if _, err := doRequest(client, req); err != nil {
		return fmt.Errorf("uploading %s: %w", filepath.Base(filePath), err)
	}
	return nil
}

// parseConanRef parses name/version[@user/channel] into a conanRef.
// Absent user/channel default to the "_" placeholder.
func parseConanRef(reference string) (conanRef, error) {
	ref := conanRef{User: conanPlaceholder, Channel: conanPlaceholder}

	nameVersion := reference
	if nv, uc, hasAt := strings.Cut(reference, "@"); hasAt {
		nameVersion = nv
		parts := strings.Split(uc, "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return conanRef{}, fmt.Errorf("user/channel must be user/channel, got: %q", uc)
		}
		ref.User, ref.Channel = parts[0], parts[1]
	}

	nv := strings.Split(nameVersion, "/")
	if len(nv) != 2 || nv[0] == "" || nv[1] == "" {
		return conanRef{}, fmt.Errorf("reference must be name/version[@user/channel], got: %q", reference)
	}
	ref.Name, ref.Version = nv[0], nv[1]

	if !conanNamePattern.MatchString(ref.Name) {
		return conanRef{}, fmt.Errorf("invalid Conan package name: %q", ref.Name)
	}
	if !conanNamePattern.MatchString(ref.Version) {
		return conanRef{}, fmt.Errorf("invalid Conan version: %q", ref.Version)
	}
	if ref.User != conanPlaceholder && !conanNamePattern.MatchString(ref.User) {
		return conanRef{}, fmt.Errorf("invalid Conan user: %q", ref.User)
	}
	if ref.Channel != conanPlaceholder && !conanNamePattern.MatchString(ref.Channel) {
		return conanRef{}, fmt.Errorf("invalid Conan channel: %q", ref.Channel)
	}
	return ref, nil
}

// collectConanFiles reads dir and returns the valid Conan files according to isValid,
// plus a list of any skipped file names. conanmanifest.txt must be present.
func collectConanFiles(dir string, isValid func(string) bool) (files []string, skipped []string, err error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("accessing directory: %w", err)
	}
	if !info.IsDir() {
		return nil, nil, fmt.Errorf("path must be a directory: %s", dir)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("reading directory: %w", err)
	}

	hasManifest := false
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !isValid(name) {
			skipped = append(skipped, name)
			continue
		}
		if name == conanManifestFile {
			hasManifest = true
		}
		files = append(files, filepath.Join(dir, name))
	}
	if !hasManifest {
		return nil, skipped, fmt.Errorf("%s not found in directory: %s", conanManifestFile, dir)
	}
	return files, skipped, nil
}

// orderConanFiles sorts files with conanmanifest.txt last (server finalization marker).
func orderConanFiles(files []string) []string {
	ordered := make([]string, len(files))
	copy(ordered, files)
	sort.Slice(ordered, func(i, j int) bool {
		iM := filepath.Base(ordered[i]) == conanManifestFile
		jM := filepath.Base(ordered[j]) == conanManifestFile
		if iM != jM {
			return !iM
		}
		return ordered[i] < ordered[j]
	})
	return ordered
}

// conanRevisionFromManifest derives a revision as the hex MD5 of conanmanifest.txt.
func conanRevisionFromManifest(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, conanManifestFile))
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", conanManifestFile, err)
	}
	sum := md5.Sum(data) //nolint:gosec
	return hex.EncodeToString(sum[:]), nil
}

// isConanRecipeFile reports whether name is a canonical recipe-layer file.
func isConanRecipeFile(name string) bool {
	name = filepath.Base(name)
	if name == conanFilePy || name == conanManifestFile {
		return true
	}
	prefix, ok := conanTarballPrefix(name)
	return ok && (prefix == conanTarballExport || prefix == conanTarballSources)
}

// isConanPackageFile reports whether name is a canonical package-layer file.
func isConanPackageFile(name string) bool {
	name = filepath.Base(name)
	if name == conanInfoTxt || name == conanManifestFile {
		return true
	}
	prefix, ok := conanTarballPrefix(name)
	return ok && prefix == conanTarballPackage
}

func conanTarballPrefix(name string) (string, bool) {
	ext := filepath.Ext(name)
	if !conanTarballExtensions[ext] {
		return "", false
	}
	return strings.TrimSuffix(name, ext), true
}
