// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Masterminds/semver/v3"

	"github.com/harness/cli/pkg/cmdctx"
)

const (
	terraformTarGzExt      = ".tar.gz"
	terraformTgzExt        = ".tgz"
	terraformZipExt        = ".zip"
	terraformMaxModuleSize = 500 * 1024 * 1024 // 500MB
)

// terraformDirSkipNames are file/dir basenames excluded when packaging a
// module directory into a .tar.gz archive.
var terraformDirSkipNames = map[string]bool{
	".git":       true,
	".terraform": true,
	".DS_Store":  true,
}

// terraformProviderFilenameRegex matches terraform-provider-{type}_{version}_{os}_{arch}.zip
// per the Provider Network Mirror Protocol naming convention.
var terraformProviderFilenameRegex = regexp.MustCompile(
	`^terraform-provider-([a-zA-Z0-9-]+)_(\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?)_([a-z0-9]+)_([a-z0-9]+)\.zip$`,
)

// pushTerraformArtifact uploads a Terraform module (.tar.gz/.tgz, or a source
// directory that gets packaged into one) or a provider binary (.zip) to the
// Harness Artifact Registry.
//
// ctx.Id      = "<registry>/<ignored-name>" — only the registry part is used
// ctx.Args[0] = local file path, or a module source directory
//
// Modules require --namespace, --name, --provider and --version.
// Providers require only --namespace; type/version/os/arch are parsed from
// the filename (terraform-provider-{type}_{version}_{os}_{arch}.zip).
func pushTerraformArtifact(ctx *cmdctx.Ctx) error {
	if len(ctx.Args) == 0 {
		return fmt.Errorf("push terraform: local file path or module directory required as positional argument")
	}
	inputPath := ctx.Args[0]

	registry, _, err := parseRegistryAndName(ctx.Id)
	if err != nil {
		return fmt.Errorf("push terraform: %w", err)
	}

	namespace := cmdctx.GetString(ctx.FlagValues, "namespace")
	if namespace == "" {
		return fmt.Errorf("push terraform: --namespace is required")
	}
	moduleName := cmdctx.GetString(ctx.FlagValues, "name")
	moduleProvider := cmdctx.GetString(ctx.FlagValues, "provider")
	moduleVersion := cmdctx.GetString(ctx.FlagValues, "version")

	pathInfo, err := os.Stat(inputPath)
	if err != nil {
		return fmt.Errorf("push terraform: failed to access package path: %w", err)
	}

	if pathInfo.IsDir() {
		archivePath, packErr := packageTerraformModuleDir(inputPath, namespace, moduleName, moduleProvider, moduleVersion)
		if packErr != nil {
			return fmt.Errorf("push terraform: %w", packErr)
		}
		defer os.RemoveAll(filepath.Dir(archivePath))

		return pushTerraformModule(ctx, registry, archivePath, namespace, moduleName, moduleProvider, moduleVersion)
	}

	switch {
	case isTerraformModuleFile(inputPath):
		return pushTerraformModule(ctx, registry, inputPath, namespace, moduleName, moduleProvider, moduleVersion)
	case isTerraformProviderFile(inputPath):
		return pushTerraformProvider(ctx, registry, inputPath, namespace)
	default:
		return fmt.Errorf("push terraform: package file must be a module (%s/%s) or provider (%s), got: %s",
			terraformTarGzExt, terraformTgzExt, terraformZipExt, filepath.Ext(inputPath))
	}
}

// packageTerraformModuleDir validates a module source directory and packages
// it into a .tar.gz archive named "{ns}-{name}-{provider}-{ver}.tar.gz" in the
// OS temp dir. The caller owns removing the returned path's parent directory.
func packageTerraformModuleDir(dir, namespace, name, moduleProvider, version string) (string, error) {
	dir = filepath.Clean(dir)
	if name == "" {
		return "", fmt.Errorf("--name is required to package a module directory")
	}
	if moduleProvider == "" {
		return "", fmt.Errorf("--provider is required to package a module directory")
	}
	if version == "" {
		return "", fmt.Errorf("--version is required to package a module directory")
	}
	if _, err := semver.NewVersion(version); err != nil {
		return "", fmt.Errorf("invalid version %q, must be SemVer 2.0.0: %w", version, err)
	}

	hasTf := false
	if err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if terraformDirSkipNames[info.Name()] || strings.Contains(info.Name(), ".tfstate") {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		// Only count .tf files at the root level (direct children of dir).
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".tf") && filepath.Dir(path) == dir {
			hasTf = true
		}
		return nil
	}); err != nil {
		return "", fmt.Errorf("failed to scan module directory: %w", err)
	}
	if !hasTf {
		return "", fmt.Errorf("module directory %q must contain at least one .tf file at the root level", dir)
	}

	tmpDir, err := os.MkdirTemp("", "harness-terraform-module-")
	if err != nil {
		return "", fmt.Errorf("failed to create temp directory for packaging: %w", err)
	}

	archiveName := fmt.Sprintf("%s-%s-%s-%s%s", namespace, name, moduleProvider, version, terraformTarGzExt)
	archivePath := filepath.Join(tmpDir, archiveName)

	if err := writeTerraformModuleArchive(archivePath, dir); err != nil {
		os.RemoveAll(tmpDir)
		return "", err
	}

	info, err := os.Stat(archivePath)
	if err != nil {
		return "", fmt.Errorf("failed to access packaged module archive: %w", err)
	}
	if info.Size() > terraformMaxModuleSize {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("packaged module archive is %d bytes, exceeds max size of %d bytes", info.Size(), terraformMaxModuleSize)
	}

	return archivePath, nil
}

// writeTerraformModuleArchive walks dir and writes its contents (skipping
// VCS/state dirs) into a gzip-compressed tar at archivePath.
func writeTerraformModuleArchive(archivePath, dir string) error {
	out, err := os.Create(archivePath)
	if err != nil {
		return fmt.Errorf("failed to create archive file: %w", err)
	}

	gzWriter := gzip.NewWriter(out)
	tarWriter := tar.NewWriter(gzWriter)

	walkErr := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path == dir {
			return nil
		}
		if terraformDirSkipNames[info.Name()] || strings.Contains(info.Name(), ".tfstate") {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() {
			return nil
		}

		relPath, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return fmt.Errorf("failed to compute relative path for %s: %w", path, relErr)
		}

		file, openErr := os.Open(path)
		if openErr != nil {
			return fmt.Errorf("failed to open %s: %w", path, openErr)
		}
		defer file.Close()

		if hdrErr := tarWriter.WriteHeader(&tar.Header{
			Name: filepath.ToSlash(relPath),
			Mode: 0o644,
			Size: info.Size(),
		}); hdrErr != nil {
			return fmt.Errorf("failed to write tar header for %s: %w", relPath, hdrErr)
		}
		if _, copyErr := io.Copy(tarWriter, file); copyErr != nil {
			return fmt.Errorf("failed to write %s to archive: %w", relPath, copyErr)
		}
		return nil
	})
	if walkErr != nil {
		return fmt.Errorf("failed to build module archive: %w", walkErr)
	}

	if err := tarWriter.Close(); err != nil {
		return fmt.Errorf("failed to finalize tar: %w", err)
	}
	if err := gzWriter.Close(); err != nil {
		return fmt.Errorf("failed to finalize gzip: %w", err)
	}
	return out.Close()
}

// isTerraformModuleFile reports whether path is a module archive (.tar.gz or .tgz).
func isTerraformModuleFile(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, terraformTarGzExt) || strings.HasSuffix(lower, terraformTgzExt)
}

// isTerraformProviderFile reports whether path is a provider archive (.zip).
func isTerraformProviderFile(path string) bool {
	return strings.HasSuffix(strings.ToLower(path), terraformZipExt)
}

// pushTerraformModule uploads a pre-built module archive to
// PUT {registryURL}/pkg/{accountID}/{registry}/terraform/v1/modules/{ns}/{name}/{provider}/{ver}.
func pushTerraformModule(ctx *cmdctx.Ctx, registry, filePath, namespace, name, moduleProvider, version string) error {
	if name == "" {
		return fmt.Errorf("--name is required for module uploads")
	}
	if moduleProvider == "" {
		return fmt.Errorf("--provider is required for module uploads")
	}
	if version == "" {
		return fmt.Errorf("--version is required for module uploads")
	}
	if _, err := semver.NewVersion(version); err != nil {
		return fmt.Errorf("invalid version %q, must be SemVer 2.0.0: %w", version, err)
	}

	fi, err := os.Stat(filePath)
	if err != nil {
		return fmt.Errorf("cannot access %q: %w", filePath, err)
	}

	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("opening %q: %w", filePath, err)
	}
	defer f.Close()

	subpath := fmt.Sprintf("%s/terraform/v1/modules/%s/%s/%s/%s", registry, namespace, name, moduleProvider, version)
	uploadURL, err := buildPkgURL(ctx.Auth.RegistryURL, ctx.Auth.AccountID, subpath)
	if err != nil {
		return fmt.Errorf("building URL: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Uploading module %s/%s/%s@%s → %s ...\n", namespace, name, moduleProvider, version, registry)

	req, err := http.NewRequest("PUT", uploadURL, f)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	setAuthHeader(req, ctx.Auth)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = fi.Size()

	if sums, sumErr := computeFileChecksums(filePath); sumErr == nil {
		setChecksumHeaders(req.Header, sums)
	}

	if _, err := doRequest(newHTTPClient(), req); err != nil {
		return fmt.Errorf("upload failed: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Successfully pushed module %s/%s/%s@%s to %s\n", namespace, name, moduleProvider, version, registry)
	return nil
}

// pushTerraformProvider uploads a provider binary as-is to
// PUT {registryURL}/pkg/{accountID}/{registry}/terraform/v1/providers/{ns}/{type}/{ver}/{filename}.
// type/version/os/arch are parsed from the filename, which must already
// follow the terraform-provider-{type}_{version}_{os}_{arch}.zip convention.
func pushTerraformProvider(ctx *cmdctx.Ctx, registry, filePath, namespace string) error {
	filename := filepath.Base(filePath)
	typeName, version, osName, arch, err := parseTerraformProviderFilename(filename)
	if err != nil {
		return err
	}
	if _, err := semver.NewVersion(version); err != nil {
		return fmt.Errorf("invalid version %q in filename, must be SemVer 2.0.0: %w", version, err)
	}

	fi, err := os.Stat(filePath)
	if err != nil {
		return fmt.Errorf("cannot access %q: %w", filePath, err)
	}

	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("opening %q: %w", filePath, err)
	}
	defer f.Close()

	subpath := fmt.Sprintf("%s/terraform/v1/providers/%s/%s/%s/%s", registry, namespace, typeName, version, filename)
	uploadURL, err := buildPkgURL(ctx.Auth.RegistryURL, ctx.Auth.AccountID, subpath)
	if err != nil {
		return fmt.Errorf("building URL: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Uploading provider %s/%s@%s (%s_%s) → %s ...\n",
		namespace, typeName, version, osName, arch, registry)

	req, err := http.NewRequest("PUT", uploadURL, f)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	setAuthHeader(req, ctx.Auth)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = fi.Size()

	if sums, sumErr := computeFileChecksums(filePath); sumErr == nil {
		setChecksumHeaders(req.Header, sums)
	}

	if _, err := doRequest(newHTTPClient(), req); err != nil {
		return fmt.Errorf("upload failed: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Successfully pushed provider %s/%s@%s (%s_%s) to %s\n",
		namespace, typeName, version, osName, arch, registry)
	return nil
}

// parseTerraformProviderFilename extracts type, version, os and arch from a
// provider filename following the
// terraform-provider-{type}_{version}_{os}_{arch}.zip convention mandated by
// the Provider Network Mirror Protocol.
func parseTerraformProviderFilename(filename string) (typeName, version, osName, arch string, err error) {
	m := terraformProviderFilenameRegex.FindStringSubmatch(filename)
	if m == nil {
		return "", "", "", "", fmt.Errorf(
			"filename %q does not match required convention terraform-provider-{type}_{version}_{os}_{arch}.zip",
			filename,
		)
	}
	return m[1], m[2], m[3], m[4], nil
}
