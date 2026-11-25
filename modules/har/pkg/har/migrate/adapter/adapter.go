package adapter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/harness/cli/modules/har/pkg/har/migrate/types"

	"github.com/google/go-containerregistry/pkg/authn"
)

type Adapter interface {
	GetKeyChain(sourcePackageHostname string) (authn.Keychain, error)
	GetConfig() types.RegistryConfig
	ValidateCredentials() (bool, error)
	GetRegistry(ctx context.Context, registry string) (types.RegistryInfo, error)
	CreateRegistryIfDoesntExist(registry string) (bool, error)
	GetPackages(registry string, artifactType types.ArtifactType, root *types.TreeNode) (
		packages []types.Package,
		err error,
	)
	GetVersions(
		p types.Package,
		node *types.TreeNode,
		registry, pkg string,
		artifactType types.ArtifactType,
	) (versions []types.Version, err error)
	GetFiles(registry string) ([]types.File, error)
	DownloadFile(registry string, uri string) (io.ReadCloser, http.Header, error)
	UploadFile(
		registry string,
		file io.ReadCloser,
		f *types.File,
		header http.Header,
		artifactName string,
		version string,
		artifactType types.ArtifactType,
		metadata map[string]interface{},
	) error
	// UploadExists reports whether the file that UploadFile would write with the same
	// arguments is already at the destination.
	UploadExists(
		ctx context.Context,
		registry string,
		f *types.File,
		artifactName string,
		version string,
		artifactType types.ArtifactType,
		metadata map[string]interface{},
	) (bool, error)
	GetOCIImagePath(registry string, packageHostname string, image string) (string, error)
	AddNPMTag(registry string, name string, version string, uri string) error
	VersionExists(
		ctx context.Context,
		p types.Package,
		registryRef, pkg, version string,
		artifactType types.ArtifactType,
	) (bool, error)
	FileExists(
		ctx context.Context,
		registryRef, pkg, version string,
		fileName *types.File,
		artifactType types.ArtifactType,
	) (bool, error)
	GetAllFilesForVersion(
		ctx context.Context,
		registryRef, pkg, version string,
	) ([]string, error)
	CreateVersion(
		registry string,
		artifactName string,
		version string,
		artifactType types.ArtifactType,
		files []*types.PackageFiles,
		metadata map[string]interface{},
	) error
	SearchFiles(registry string) ([]types.SearchedFile, error)
	BuildExistingIndex(ctx context.Context, registryRef string, concurrency int) (*types.ExistingIndex, error)
}

// PagedLister is implemented by adapters whose repository listing is a long sequence
// of requests. progress receives a status line at a bounded rate, including retries.
type PagedLister interface {
	GetFilesWithProgress(registry string, progress func(string)) ([]types.File, error)

	// ListBatches calls fn with each batch of at least batchSize components (fewer for
	// the last one) while the listing is still in progress. batch is an Adapter whose
	// GetFiles, GetPackages and GetVersions cover only that batch's components. A
	// listing error is returned after the batches that preceded it were handed to fn;
	// an error from fn stops the listing.
	ListBatches(
		ctx context.Context,
		registry string,
		batchSize int,
		progress func(string),
		fn func(batch Adapter, files []types.File) error,
	) error
}

var registry = map[types.RegistryType]Factory{}

type Factory interface {
	Create(ctx context.Context, config types.RegistryConfig) (Adapter, error)
}

// RegisterFactory registers one adapter factory to the registry.
func RegisterFactory(t types.RegistryType, factory Factory) error {
	if len(t) == 0 {
		return errors.New("invalid type")
	}
	if factory == nil {
		return errors.New("empty adapter factory")
	}

	if _, exist := registry[t]; exist {
		return fmt.Errorf("adapter factory for %s already exists", t)
	}
	registry[t] = factory
	return nil
}

// GetFactory gets the adapter factory by the specified name.
func GetFactory(t types.RegistryType) (Factory, error) {
	factory, exist := registry[t]
	if !exist {
		return nil, fmt.Errorf("adapter factory for %s not found", t)
	}
	return factory, nil
}

func GetAdapter(ctx context.Context, cfg types.RegistryConfig) (Adapter, error) {
	factory, err := GetFactory(cfg.Type)
	if err != nil {
		return nil, fmt.Errorf("failed to get adapter factory: %v", err)
	}
	adapter, err := factory.Create(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create adapter: %v", err)
	}
	return adapter, nil
}
