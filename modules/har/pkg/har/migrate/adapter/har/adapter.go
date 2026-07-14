package har

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

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
}

func (f factory) Create(_ context.Context, cfg types.RegistryConfig) (adp.Adapter, error) {
	return newAdapter(cfg)
}

func newAdapter(cfg types.RegistryConfig) (adp.Adapter, error) {
	c := newClient(&cfg)
	logger := log.With().Str("adapter", "HAR").Logger()
	return &harAdapter{
		client: c,
		reg:    cfg,
		logger: logger,
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
	return a.client.getRegistry(ctx, registry)
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
	case types.RAW:
		err = a.client.uploadRawFile(registry, f, file)
	}
	if err != nil {
		a.logger.Error().Err(err).Msgf("Failed to upload file %s to registry: %s", f.Uri, registry)
		return fmt.Errorf("failed to upload file %s to registry: %s, %v", f.Uri, registry, err)
	}
	return nil
}

func (a *harAdapter) GetOCIImagePath(registry string, _ string, image string) (string, error) {
	parse, _ := url.Parse(a.reg.Endpoint)
	return util.GenOCIImagePath(parse.Host, strings.ToLower(a.reg.AccountID), registry, image), nil
}

func (a *harAdapter) AddNPMTag(registry string, name string, version string, uri string) error {
	return a.client.AddNPMTag(registry, name, version, uri)
}

func (a *harAdapter) VersionExists(ctx context.Context, p types.Package, registryRef, pkg, version string, artifactType types.ArtifactType) (bool, error) {
	if artifactType == types.HELM_LEGACY {
		artifactType = types.HELM
	}
	return a.client.artifactVersionExists(ctx, registryRef, pkg, version, artifactType)
}

func (a *harAdapter) FileExists(ctx context.Context, registryRef, pkg, version string, file *types.File, artifactType types.ArtifactType) (bool, error) {
	if artifactType == types.RAW {
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
