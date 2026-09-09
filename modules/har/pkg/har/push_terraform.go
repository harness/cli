// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/harness/cli/v3/pkg/cmdctx"
)


const (
	terraformTarGzExt = ".tar.gz"
	terraformTgzExt   = ".tgz"
	terraformZipExt   = ".zip"

	// terraformMaxModuleSize caps a locally packaged module archive. Uploading a
	// larger archive is rejected server-side, so fail before spending the upload.
	terraformMaxModuleSize = 500 * 1024 * 1024
)

// terraformSkipNames are basenames excluded when packaging a module directory:
// VCS metadata and local Terraform state that must never reach the registry.
var terraformSkipNames = map[string]bool{
	".git":       true,
	".terraform": true,
	".DS_Store":  true,
}

// terraformProviderFilenameRegex matches terraform-provider-{type}_{version}_{os}_{arch}.zip,
// the naming convention mandated by the Provider Network Mirror Protocol. The
// upload path is derived entirely from these captures, so a non-conforming name
// cannot be published.
var terraformProviderFilenameRegex = regexp.MustCompile(
	`^terraform-provider-([a-zA-Z0-9-]+)_(\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?)_([a-z0-9]+)_([a-z0-9]+)\.zip$`,
)

// pushTerraformArtifact implements "push artifact:terraform".
//
// Accepts three input shapes:
//   - module archive (.tar.gz/.tgz) → PUT /pkg/{account}/{registry}/terraform/v1/modules/{ns}/{name}/{provider}/{version}
//   - module directory             → packaged into a .tar.gz, then uploaded as above
//   - provider binary (.zip)       → PUT /pkg/{account}/{registry}/terraform/v1/providers/{ns}/{type}/{version}/{filename}
//
// Modules require --namespace, --name, --provider and --version. Providers require
// only --namespace; type, version, os and arch are parsed from the filename.
func pushTerraformArtifact(ctx *cmdctx.Ctx) error {
	if len(ctx.Args) == 0 {
		return fmt.Errorf("push terraform artifact requires a file or directory path: push artifact:terraform <registry> <path>")
	}

	registry := ctx.Id
	inputPath := ctx.Args[0]

	namespace := cmdctx.GetString(ctx.FlagValues, "namespace")
	if namespace == "" {
		return fmt.Errorf("--namespace is required")
	}
	name := cmdctx.GetString(ctx.FlagValues, "name")
	provider := cmdctx.GetString(ctx.FlagValues, "provider")
	version := cmdctx.GetString(ctx.FlagValues, "version")

	// Directories are always literal paths — glob expansion does not apply.
	if info, err := os.Stat(inputPath); err == nil && info.IsDir() {
		if err := validateTerraformModuleIdentity(name, provider, version); err != nil {
			return err
		}
		archivePath, cleanup, err := packageTerraformModuleDir(inputPath, namespace, name, provider, version)
		if err != nil {
			return err
		}
		defer cleanup()
		return uploadTerraformModule(ctx, registry, archivePath, namespace, name, provider, version)
	}

	filePath, err := resolveTerraformFilePath(inputPath)
	if err != nil {
		return err
	}

	lower := strings.ToLower(filePath)
	switch {
	case strings.HasSuffix(lower, terraformTarGzExt), strings.HasSuffix(lower, terraformTgzExt):
		if err := validateTerraformModuleIdentity(name, provider, version); err != nil {
			return err
		}
		return uploadTerraformModule(ctx, registry, filePath, namespace, name, provider, version)
	case strings.HasSuffix(lower, terraformZipExt):
		return uploadTerraformProvider(ctx, registry, filePath, namespace)
	default:
		return fmt.Errorf("unsupported file type %q: must be a module (%s or %s) or a provider (%s)",
			filepath.Base(filePath), terraformTarGzExt, terraformTgzExt, terraformZipExt)
	}
}

// resolveTerraformFilePath resolves inputPath to a single file. If inputPath
// contains glob wildcards, it expands them and returns the first match that
// has a recognised Terraform extension (.tar.gz, .tgz, .zip). If no wildcards
// are present, it validates the path exists and returns it as-is.
func resolveTerraformFilePath(inputPath string) (string, error) {
	if !strings.ContainsAny(inputPath, "*?[") {
		if _, err := os.Stat(inputPath); err != nil {
			return "", fmt.Errorf("cannot access %q: %w", inputPath, err)
		}
		return inputPath, nil
	}

	matches, err := filepath.Glob(inputPath)
	if err != nil {
		return "", fmt.Errorf("invalid glob pattern %q: %w", inputPath, err)
	}
	for _, m := range matches {
		lower := strings.ToLower(m)
		if strings.HasSuffix(lower, terraformTarGzExt) || strings.HasSuffix(lower, terraformTgzExt) || strings.HasSuffix(lower, terraformZipExt) {
			return m, nil
		}
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("no files matched pattern %q", inputPath)
	}
	return "", fmt.Errorf("no Terraform files (%s, %s, %s) matched pattern %q", terraformTarGzExt, terraformTgzExt, terraformZipExt, inputPath)
}

// validateTerraformModuleIdentity checks the flags that make up a module's
// coordinates. Providers derive these from the filename instead, so this applies
// to module uploads only.
func validateTerraformModuleIdentity(name, provider, version string) error {
	if name == "" {
		return fmt.Errorf("--name is required for module uploads")
	}
	if provider == "" {
		return fmt.Errorf("--provider is required for module uploads")
	}
	if version == "" {
		return fmt.Errorf("--version is required for module uploads")
	}
	if _, err := semver.NewVersion(version); err != nil {
		return fmt.Errorf("invalid --version %q, must be SemVer 2.0.0: %w", version, err)
	}
	return nil
}

// uploadTerraformModule uploads a pre-built module archive.
//
// URL: {registryURL}/pkg/{accountID}/{registry}/terraform/v1/modules/{ns}/{name}/{provider}/{version}
func uploadTerraformModule(ctx *cmdctx.Ctx, registry, filePath, namespace, name, provider, version string) error {
	subpath := fmt.Sprintf("%s/terraform/v1/modules/%s/%s/%s/%s",
		url.PathEscape(registry),
		url.PathEscape(namespace),
		url.PathEscape(name),
		url.PathEscape(provider),
		url.PathEscape(version),
	)
	if err := terraformPutFile(ctx, filePath, subpath); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Successfully pushed Terraform module %s/%s/%s@%s to registry %q\n",
		namespace, name, provider, version, registry)
	return nil
}

// uploadTerraformProvider uploads a provider binary as-is. Its coordinates come
// from the filename rather than flags.
//
// URL: {registryURL}/pkg/{accountID}/{registry}/terraform/v1/providers/{ns}/{type}/{version}/{filename}
func uploadTerraformProvider(ctx *cmdctx.Ctx, registry, filePath, namespace string) error {
	filename := filepath.Base(filePath)
	typeName, version, osName, arch, err := parseTerraformProviderFilename(filename)
	if err != nil {
		return err
	}
	if _, err := semver.NewVersion(version); err != nil {
		return fmt.Errorf("invalid version %q in filename, must be SemVer 2.0.0: %w", version, err)
	}

	subpath := fmt.Sprintf("%s/terraform/v1/providers/%s/%s/%s/%s",
		url.PathEscape(registry),
		url.PathEscape(namespace),
		url.PathEscape(typeName),
		url.PathEscape(version),
		url.PathEscape(filename),
	)
	if err := terraformPutFile(ctx, filePath, subpath); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Successfully pushed Terraform provider %s/%s@%s (%s_%s) to registry %q\n",
		namespace, typeName, version, osName, arch, registry)
	return nil
}

// terraformPutFile streams filePath to subpath as an octet-stream PUT with
// checksum headers.
func terraformPutFile(ctx *cmdctx.Ctx, filePath, subpath string) error {
	if err := validateRegularFile(filePath); err != nil {
		return err
	}

	checksums, err := computeFileChecksums(filePath)
	if err != nil {
		return fmt.Errorf("computing checksums: %w", err)
	}

	uploadURL, err := buildPkgURL(ctx.Auth.RegistryURL, ctx.Auth.AccountID, subpath)
	if err != nil {
		return err
	}

	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("opening %q: %w", filePath, err)
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat %q: %w", filePath, err)
	}

	fmt.Fprintf(os.Stderr, "Uploading %s (%s) ...\n", filepath.Base(filePath), formatBytes(fi.Size()))

	req, err := http.NewRequest("PUT", uploadURL, f)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	setAuthHeader(req, ctx.Auth)
	req.Header.Set("Content-Type", "application/octet-stream")
	setChecksumHeaders(req.Header, checksums)
	req.ContentLength = fi.Size()

	if _, err := doRequest(newHTTPClient(), req); err != nil {
		return fmt.Errorf("upload failed: %w", err)
	}
	return nil
}

// packageTerraformModuleDir packages a module source directory into a .tar.gz in
// a temp dir and returns its path plus a cleanup func the caller must defer.
func packageTerraformModuleDir(dir, namespace, name, provider, version string) (string, func(), error) {
	dir = filepath.Clean(dir)

	hasRootTF, err := dirHasRootTerraformFile(dir)
	if err != nil {
		return "", nil, err
	}
	if !hasRootTF {
		return "", nil, fmt.Errorf("module directory %q must contain at least one .tf file at its root", dir)
	}

	tmpDir, err := os.MkdirTemp("", "harness-terraform-module-")
	if err != nil {
		return "", nil, fmt.Errorf("creating temp directory for packaging: %w", err)
	}
	cleanup := func() { os.RemoveAll(tmpDir) }

	archivePath := filepath.Join(tmpDir, fmt.Sprintf("%s-%s-%s-%s%s", namespace, name, provider, version, terraformTarGzExt))

	fmt.Fprintf(os.Stderr, "Packaging module directory %s ...\n", dir)
	if err := writeTerraformModuleArchive(archivePath, dir); err != nil {
		cleanup()
		return "", nil, err
	}

	info, err := os.Stat(archivePath)
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("cannot access packaged module archive: %w", err)
	}
	if info.Size() > terraformMaxModuleSize {
		cleanup()
		return "", nil, fmt.Errorf("packaged module archive is %s, which exceeds the %s limit",
			formatBytes(info.Size()), formatBytes(terraformMaxModuleSize))
	}

	return archivePath, cleanup, nil
}

// dirHasRootTerraformFile reports whether dir contains a .tf file as a direct
// child. Nested .tf files don't make a valid module root.
func dirHasRootTerraformFile(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, fmt.Errorf("reading module directory %q: %w", dir, err)
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".tf") {
			return true, nil
		}
	}
	return false, nil
}

// writeTerraformModuleArchive walks dir and writes its regular files into a
// gzipped tar at archivePath, preserving relative layout.
func writeTerraformModuleArchive(archivePath, dir string) error {
	out, err := os.Create(archivePath)
	if err != nil {
		return fmt.Errorf("creating archive file: %w", err)
	}

	gzWriter := gzip.NewWriter(out)
	tarWriter := tar.NewWriter(gzWriter)

	walkErr := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == dir {
			return nil
		}
		if terraformSkipNames[d.Name()] || strings.Contains(d.Name(), ".tfstate") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("stat %s: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return nil
		}

		relPath, err := filepath.Rel(dir, path)
		if err != nil {
			return fmt.Errorf("computing relative path for %s: %w", path, err)
		}

		return addFileToTar(tarWriter, path, filepath.ToSlash(relPath), info)
	})
	if walkErr != nil {
		return fmt.Errorf("building module archive: %w", walkErr)
	}

	if err := tarWriter.Close(); err != nil {
		return fmt.Errorf("finalising tar: %w", err)
	}
	if err := gzWriter.Close(); err != nil {
		return fmt.Errorf("finalising gzip: %w", err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("flushing archive to disk: %w", err)
	}
	return nil
}

// addFileToTar writes one file into tw. Split out so the opened file is closed
// on each iteration rather than at the end of the whole walk.
func addFileToTar(tw *tar.Writer, path, relPath string, info os.FileInfo) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()

	if err := tw.WriteHeader(&tar.Header{
		Name:    relPath,
		Mode:    0o644,
		Size:    info.Size(),
		ModTime: info.ModTime(),
	}); err != nil {
		return fmt.Errorf("writing tar header for %s: %w", relPath, err)
	}
	if _, err := io.Copy(tw, f); err != nil {
		return fmt.Errorf("writing %s into archive: %w", relPath, err)
	}
	return nil
}

// parseTerraformProviderFilename extracts type, version, os and arch from a
// provider filename.
func parseTerraformProviderFilename(filename string) (typeName, version, osName, arch string, err error) {
	m := terraformProviderFilenameRegex.FindStringSubmatch(filename)
	if m == nil {
		return "", "", "", "", fmt.Errorf(
			"provider filename %q does not match the required convention terraform-provider-{type}_{version}_{os}_{arch}.zip",
			filename,
		)
	}
	return m[1], m[2], m[3], m[4], nil
}


