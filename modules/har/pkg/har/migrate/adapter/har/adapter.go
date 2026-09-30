package har

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"

	adp "github.com/harness/cli/modules/har/pkg/har/migrate/adapter"
	"github.com/harness/cli/modules/har/pkg/har/migrate/types"
	"github.com/harness/cli/modules/har/pkg/har/migrate/util"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func init() {
	if err := adp.RegisterFactory(types.HAR, new(factory)); err != nil {
		return
	}
}

type factory struct{}

type harAdapter struct {
	client *client
	reg    types.RegistryConfig
	logger zerolog.Logger

	// registryURLMu guards registryURLCache, which maps registry name → the
	// URL returned by getRegistry (e.g. "https://host/oci/helmoci"). Populated
	// by GetRegistry during the pre-migration step and read by GetOCIImagePath
	// to derive the correct OCI path prefix without a second API call.
	registryURLMu    sync.Mutex
	registryURLCache map[string]string
}

func (f factory) Create(_ context.Context, cfg types.RegistryConfig) (adp.Adapter, error) {
	return newAdapter(cfg)
}

func newAdapter(cfg types.RegistryConfig) (adp.Adapter, error) {
	c := newClient(&cfg)
	logger := log.With().Str("adapter", "HAR").Logger()
	return &harAdapter{
		client:           c,
		reg:              cfg,
		logger:           logger,
		registryURLCache: make(map[string]string),
	}, nil
}

func (a *harAdapter) GetKeyChain(_ string) (authn.Keychain, error) {
	parseUrl, err := url.Parse(a.reg.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to parse [%s], err: %w", a.reg.Endpoint, err)
	}
	return NewHarKeychain(a.reg.Credentials.Username, a.reg.Credentials.Password, parseUrl.Host), nil
}

func (a *harAdapter) GetConfig() types.RegistryConfig { return a.reg }

func (a *harAdapter) ValidateCredentials() (bool, error) { return false, nil }

func (a *harAdapter) GetRegistry(ctx context.Context, registry string) (types.RegistryInfo, error) {
	info, err := a.client.getRegistry(ctx, registry)
	if err != nil {
		return info, err
	}
	if info.URL != "" {
		a.registryURLMu.Lock()
		a.registryURLCache[registry] = info.URL
		a.registryURLMu.Unlock()
	}
	return info, nil
}

func (a *harAdapter) CreateRegistryIfDoesntExist(_ string) (bool, error) { return false, nil }

func (a *harAdapter) GetPackages(registry string, artifactType types.ArtifactType, root *types.TreeNode) ([]types.Package, error) {
	return nil, nil
}

func (a *harAdapter) GetVersions(p types.Package, node *types.TreeNode, registry, pkg string, artifactType types.ArtifactType) ([]types.Version, error) {
	return nil, nil
}

func (a *harAdapter) GetFiles(_ string) ([]types.File, error) { return nil, nil }

func (a *harAdapter) DownloadFile(_ string, _ string) (io.ReadCloser, http.Header, error) {
	return nil, http.Header{}, nil
}

func (a *harAdapter) UploadFile(
	registry string,
	file io.ReadCloser,
	f *types.File,
	header http.Header,
	artifactName string,
	version string,
	artifactType types.ArtifactType,
	metadata map[string]interface{},
) error {
	a.logger.Debug().Msgf("Uploading file %s to registry: %s", f.Uri, registry)
	var err error
	switch artifactType {
	case types.GENERIC:
		err = a.client.uploadGenericFile(registry, artifactName, version, f, file)
	case types.MAVEN:
		err = a.client.uploadMavenFile(registry, artifactName, version, f, file)
	case types.PYTHON:
		err = a.client.uploadPythonFile(registry, artifactName, version, f, file, metadata)
	case types.NUGET:
		err = a.client.uploadNugetFile(registry, artifactName, version, f, file)
	case types.NPM:
		err = a.client.uploadNPMFile(registry, artifactName, version, f, file)
	case types.RPM:
		err = a.client.uploadRPMFile(registry, f.Name, file)
	case types.CONDA:
		err = a.client.uploadCondaFile(registry, f.Name, file, metadata)
	case types.COMPOSER:
		err = a.client.uploadComposerFile(registry, f.Name, file)
	case types.SWIFT:
		err = a.client.uploadSwiftFile(registry, f.Name, file, artifactName, version)
	case types.DART:
		err = a.client.uploadDartFile(registry, artifactName, version, f, file)
	case types.RAW, types.CRAN:
		err = a.client.uploadRawFile(registry, f, file)
	case types.DEBIAN:
		err = a.client.uploadDebianFile(registry, f, file, metadata)
	case types.PUPPET:
		err = a.client.uploadPuppetFile(registry, f, file)
	case types.RUBY:
		err = a.client.uploadRubyFile(registry, f, file)
	case types.TERRAFORM:
		err = a.client.uploadTerraformFile(registry, f, artifactName, version, file)
	case types.CONAN:
		err = a.client.uploadConanFile(registry, file, metadata)
	default:
		return fmt.Errorf("unsupported artifact type for upload: %s", artifactType)
	}
	if err != nil {
		if errors.Is(err, types.ErrArtifactAlreadyExists) {
			return err
		}
		a.logger.Error().Err(err).Msgf("Failed to upload file %s to registry: %s", f.Uri, registry)
		return fmt.Errorf("failed to upload file %s to registry: %s, %w", f.Uri, registry, err)
	}
	return nil
}

func (a *harAdapter) GetOCIImagePath(registry string, _ string, image string) (string, error) {
	parse, err := url.Parse(a.reg.Endpoint)
	if err != nil {
		return "", fmt.Errorf("failed to parse [%s], err: %w", a.reg.Endpoint, err)
	}
	return util.GenOCIImagePath(parse.Host, a.ociPrefixFromCache(registry), registry, image), nil
}

// ociPrefixFromCache derives the OCI path prefix (e.g. "oci" or lowercased
// accountID) from the registry URL cached by GetRegistry during the pre-step.
//
// Example: URL "https://pkg.harness.io/oci/myreg", registry "myreg"
// → strip scheme+host → "/oci/myreg" → strip last segment → "/oci" → "oci"
//
// Falls back to the lowercased accountID when no cached URL is available,
// which preserves the behaviour on environments where VanityURLRegistryEnabled
// is off and the pre-step was skipped (e.g. dry-run).
func (a *harAdapter) ociPrefixFromCache(registry string) string {
	a.registryURLMu.Lock()
	registryURL := a.registryURLCache[registry]
	a.registryURLMu.Unlock()

	if registryURL != "" {
		parsed, err := url.Parse(registryURL)
		if err == nil {
			// path is e.g. "/oci/helmoci" — strip the last segment (registry name)
			// to get the prefix segment(s).
			p := strings.TrimSuffix(parsed.Path, "/"+registry)
			p = strings.Trim(p, "/")
			if p != "" {
				return p
			}
		}
	}

	return strings.ToLower(a.reg.AccountID)
}

func (a *harAdapter) AddNPMTag(registry string, name string, version string, uri string) error {
	return a.client.AddNPMTag(registry, name, version, uri)
}

func (a *harAdapter) VersionExists(ctx context.Context, p types.Package, registryRef, pkg, version string, artifactType types.ArtifactType) (bool, error) {
	if artifactType == types.HELM_LEGACY {
		artifactType = types.HELM
	}
	if artifactType == types.HELM_HTTP {
		// HAR stores the chart by its leaf name; nested names like "team-a/abc" must resolve to "abc".
		pkg = path.Base(pkg)
	}
	return a.client.artifactVersionExists(ctx, registryRef, pkg, version, artifactType)
}

func (a *harAdapter) FileExists(ctx context.Context, registryRef, pkg, version string, file *types.File, artifactType types.ArtifactType) (bool, error) {
	if artifactType == types.RAW || artifactType == types.CRAN {
		return a.client.headRawFile(registryRef, file.Uri)
	}
	return a.client.artifactFileExists(ctx, registryRef, pkg, version, file, artifactType)
}

func (a *harAdapter) GetAllFilesForVersion(ctx context.Context, registryRef, pkg, version string) ([]string, error) {
	return a.client.artifactGetFilesForVersion(ctx, registryRef, pkg, version)
}

func (a *harAdapter) CreateVersion(registry string, artifactName string, version string, artifactType types.ArtifactType, files []*types.PackageFiles, _ map[string]interface{}) error {
	switch artifactType {
	case types.GO:
		return a.client.createGoVersion(registry, artifactName, version, files)
	default:
		return fmt.Errorf("not implemented")
	}
}

func (a *harAdapter) SearchFiles(_ string) ([]types.SearchedFile, error) {
	return nil, fmt.Errorf("date filter (SearchFiles) is not supported for this source adapter")
}
func (a *harAdapter) BuildExistingIndex(ctx context.Context, registryRef string, concurrency int) (*types.ExistingIndex, error) {
	return a.client.buildExistingIndex(ctx, registryRef, concurrency)
}
