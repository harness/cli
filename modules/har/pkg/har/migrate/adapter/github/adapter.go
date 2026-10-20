// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package github

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"

	adp "github.com/harness/cli/modules/har/pkg/har/migrate/adapter"
	"github.com/harness/cli/modules/har/pkg/har/migrate/tree"
	"github.com/harness/cli/modules/har/pkg/har/migrate/types"

	"github.com/google/go-containerregistry/pkg/authn"
)

const (
	githubAPIURL = "https://api.github.com"
	githubNpmURL = "https://npm.pkg.github.com"
)

func init() {
	if err := adp.RegisterFactory(types.GITHUB, new(factory)); err != nil {
		return
	}
}

type factory struct{}

func (f factory) Create(ctx context.Context, config types.RegistryConfig) (adp.Adapter, error) {
	return newAdapter(ctx, config)
}

type adapter struct {
	client *client
	reg    types.RegistryConfig
	// tarballURLs maps the synthetic file URI returned by GetFiles to the
	// packument's dist.tarball URL, which DownloadFile needs.
	tarballURLs sync.Map
}

func newAdapter(ctx context.Context, config types.RegistryConfig) (adp.Adapter, error) {
	apiURL, npmURL, err := registryURLs(config.Endpoint)
	if err != nil {
		return nil, err
	}
	return &adapter{
		client: newClient(ctx, apiURL, npmURL, config.Credentials.Password, config.Insecure),
		reg:    config,
	}, nil
}

func registryURLs(endpoint string) (apiURL, npmURL string, err error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", "", fmt.Errorf("failed to parse GitHub endpoint %q: %w", endpoint, err)
	}
	switch strings.ToLower(u.Hostname()) {
	case "github.com", "api.github.com":
		return githubAPIURL, githubNpmURL, nil
	}
	return "", "", fmt.Errorf("unsupported GitHub endpoint %q: only github.com (https://api.github.com) is supported", endpoint)
}

type npmVersion struct {
	scopedName string
	version    string
	tarball    string
	published  string
}

// uri mirrors JFrog's npm layout (/@scope/name/-/@scope/name-1.0.0.tgz) so the
// engine's tree, .tgz filter and pattern filters behave as they do for JFrog.
func (v npmVersion) uri() string {
	return "/" + v.scopedName + "/-/" + v.scopedName + "-" + v.version + ".tgz"
}

func (v npmVersion) fileName() string {
	return v.shortName() + "-" + v.version + ".tgz"
}

func (v npmVersion) shortName() string {
	return v.scopedName[strings.Index(v.scopedName, "/")+1:]
}

func (v npmVersion) scope() string {
	return v.scopedName[:strings.Index(v.scopedName, "/")]
}

// listVersions returns every version of every npm package owned by owner. All
// GitHub npm packages are scoped to their lowercased owner.
func (a *adapter) listVersions(owner string) ([]npmVersion, error) {
	pkgs, err := a.client.listNpmPackages(owner)
	if err != nil {
		return nil, err
	}
	scope := "@" + strings.ToLower(owner)
	var out []npmVersion
	for _, pkg := range pkgs {
		scopedName := scope + "/" + pkg.Name
		meta, err := a.client.getPackument(scopedName)
		if err != nil {
			return nil, err
		}
		versions := make([]string, 0, len(meta.Versions))
		for v := range meta.Versions {
			versions = append(versions, v)
		}
		sort.Strings(versions)
		for _, v := range versions {
			tarball := meta.Versions[v].Dist.Tarball
			if tarball == "" {
				return nil, fmt.Errorf("npm metadata for %s@%s has no dist.tarball", scopedName, v)
			}
			out = append(out, npmVersion{
				scopedName: scopedName,
				version:    v,
				tarball:    tarball,
				published:  meta.Time[v],
			})
		}
	}
	return out, nil
}

func (a *adapter) GetFiles(owner string) ([]types.File, error) {
	versions, err := a.listVersions(owner)
	if err != nil {
		return nil, err
	}
	files := make([]types.File, 0, len(versions))
	for _, v := range versions {
		uri := v.uri()
		a.tarballURLs.Store(uri, v.tarball)
		files = append(files, types.File{
			Registry:     owner,
			Name:         v.fileName(),
			Uri:          uri,
			LastModified: v.published,
		})
	}
	return files, nil
}

func (a *adapter) DownloadFile(_ string, uri string) (io.ReadCloser, http.Header, error) {
	tarball, ok := a.tarballURLs.Load(uri)
	if !ok {
		return nil, nil, fmt.Errorf("no GitHub tarball URL recorded for %s", uri)
	}
	return a.client.download(tarball.(string))
}

// SearchFiles supports dateFilter.createdAfter using each version's publish time.
// GitHub exposes no download timestamps, so downloadedAfter never matches.
func (a *adapter) SearchFiles(owner string) ([]types.SearchedFile, error) {
	versions, err := a.listVersions(owner)
	if err != nil {
		return nil, err
	}
	out := make([]types.SearchedFile, 0, len(versions))
	for _, v := range versions {
		out = append(out, types.SearchedFile{
			Repo:     owner,
			Path:     v.scopedName + "/-/" + v.scope(),
			Name:     v.fileName(),
			Created:  v.published,
			Modified: v.published,
		})
	}
	return out, nil
}

// parseTarballURI is the inverse of npmVersion.uri.
func parseTarballURI(uri string) (v npmVersion, ok bool) {
	parts := strings.Split(strings.TrimPrefix(uri, "/"), "/")
	if len(parts) != 5 || parts[2] != "-" || parts[0] != parts[3] {
		return npmVersion{}, false
	}
	prefix := parts[1] + "-"
	file := parts[4]
	if !strings.HasPrefix(file, prefix) || !strings.HasSuffix(file, ".tgz") {
		return npmVersion{}, false
	}
	version := strings.TrimSuffix(strings.TrimPrefix(file, prefix), ".tgz")
	if version == "" {
		return npmVersion{}, false
	}
	return npmVersion{scopedName: parts[0] + "/" + parts[1], version: version}, true
}

// treeVersions derives versions from the tree rather than from GitHub, so
// packages and versions pruned by date or pattern filters are not reported.
func treeVersions(node *types.TreeNode) ([]npmVersion, error) {
	files, err := tree.GetAllFiles(node)
	if err != nil {
		return nil, err
	}
	out := make([]npmVersion, 0, len(files))
	for _, f := range files {
		if v, ok := parseTarballURI(f.Uri); ok {
			out = append(out, v)
		}
	}
	return out, nil
}

func (a *adapter) GetPackages(owner string, artifactType types.ArtifactType, root *types.TreeNode) ([]types.Package, error) {
	if err := types.ValidateSourceArtifactType(types.GITHUB, artifactType); err != nil {
		return nil, err
	}
	versions, err := treeVersions(root)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var pkgs []types.Package
	for _, v := range versions {
		if seen[v.scopedName] {
			continue
		}
		seen[v.scopedName] = true
		pkgs = append(pkgs, types.Package{Registry: owner, Name: v.scopedName, Path: "/" + v.scopedName, Size: -1})
	}
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].Name < pkgs[j].Name })
	return pkgs, nil
}

// GetVersions points every version at the package's shared "-/@scope" node;
// Version.Migrate then keeps only that version's tarball.
func (a *adapter) GetVersions(
	_ types.Package, node *types.TreeNode, owner, pkg string, artifactType types.ArtifactType,
) ([]types.Version, error) {
	if err := types.ValidateSourceArtifactType(types.GITHUB, artifactType); err != nil {
		return nil, err
	}
	versions, err := treeVersions(node)
	if err != nil {
		return nil, err
	}
	var out []types.Version
	for _, v := range versions {
		if v.scopedName != pkg {
			continue
		}
		out = append(out, types.Version{Registry: owner, Pkg: pkg, Name: v.version, Path: "-/" + v.scope(), Size: -1})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (a *adapter) GetConfig() types.RegistryConfig {
	return a.reg
}

func (a *adapter) ValidateCredentials() (bool, error) {
	if err := a.client.validate(); err != nil {
		return false, fmt.Errorf("failed to validate GitHub credentials: %w", err)
	}
	return true, nil
}

func (a *adapter) GetRegistry(_ context.Context, owner string) (types.RegistryInfo, error) {
	return types.RegistryInfo{Type: "github", URL: a.reg.Endpoint, Path: owner}, nil
}

func (a *adapter) CreateRegistryIfDoesntExist(_ string) (bool, error) {
	return false, nil
}

// --- Stubs for operations not applicable to a source-only GitHub adapter ---

func (a *adapter) GetKeyChain(_ string) (authn.Keychain, error) {
	return nil, fmt.Errorf("GetKeyChain not implemented for GITHUB")
}

func (a *adapter) GetOCIImagePath(_, _, _ string) (string, error) {
	return "", fmt.Errorf("GetOCIImagePath not implemented for GITHUB")
}

func (a *adapter) UploadFile(_ string, _ io.ReadCloser, _ *types.File, _ http.Header, _, _ string, _ types.ArtifactType, _ map[string]interface{}) error {
	return fmt.Errorf("UploadFile not implemented for GITHUB")
}

func (a *adapter) AddNPMTag(_, _, _, _ string) error {
	return nil
}

func (a *adapter) VersionExists(_ context.Context, _ types.Package, _, _, _ string, _ types.ArtifactType) (bool, error) {
	return false, fmt.Errorf("VersionExists not implemented for GITHUB")
}

func (a *adapter) FileExists(_ context.Context, _, _, _ string, _ *types.File, _ types.ArtifactType) (bool, error) {
	return false, fmt.Errorf("FileExists not implemented for GITHUB")
}

func (a *adapter) GetAllFilesForVersion(_ context.Context, _, _, _ string) ([]string, error) {
	return nil, fmt.Errorf("GetAllFilesForVersion not implemented for GITHUB")
}

func (a *adapter) CreateVersion(_ string, _ string, _ string, _ types.ArtifactType, _ []*types.PackageFiles, _ map[string]interface{}) error {
	return fmt.Errorf("CreateVersion not implemented for GITHUB")
}

func (a *adapter) BuildExistingIndex(_ context.Context, _ string, _ int) (*types.ExistingIndex, error) {
	return nil, nil
}
