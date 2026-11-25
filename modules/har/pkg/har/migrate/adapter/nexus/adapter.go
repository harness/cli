package nexus

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
	"github.com/rs/zerolog/log"
)

const (
	// Nexus V3 specific constants
	nexusAPIVersion = "v1"
)

func init() {
	adapterType := types.NEXUS
	if err := adp.RegisterFactory(adapterType, new(factory)); err != nil {
		return
	}
}

// factory section
type factory struct {
}

// Create an adapter section
func (f factory) Create(ctx context.Context, config types.RegistryConfig) (adp.Adapter, error) {
	return newAdapter(config)
}

// adapter section
type adapter struct {
	client   *client
	reg      types.RegistryConfig
	listings listingCache
}

func newAdapter(config types.RegistryConfig) (adp.Adapter, error) {
	return &adapter{
		client: newClient(&config),
		reg:    config,
	}, nil
}

func (a *adapter) GetKeyChain(sourcePackageHostname string) (authn.Keychain, error) {
	host := sourcePackageHostname
	if host == "" {
		parseUrl, err := url.Parse(a.reg.Endpoint)
		if err != nil {
			return nil, fmt.Errorf("failed to parse [%s], err: %w", a.reg.Endpoint, err)
		}
		host = parseUrl.Host
	}
	return NewNexusKeychain(a.reg.Credentials.Username, a.reg.Credentials.Password, host), nil
}

func (a *adapter) GetConfig() types.RegistryConfig {
	return a.reg
}

func (a *adapter) ValidateCredentials() (bool, error) {
	// Try to get repositories to validate credentials
	_, err := a.client.getRepositories()
	if err != nil {
		return false, fmt.Errorf("failed to validate credentials: %w", err)
	}
	return true, nil
}

func (a *adapter) GetRegistry(ctx context.Context, registry string) (types.RegistryInfo, error) {
	repo, err := a.client.getRepository(registry)
	if err != nil {
		return types.RegistryInfo{}, fmt.Errorf("failed to get repository %s: %w", registry, err)
	}

	return types.RegistryInfo{
		Type: repo.Format,
		URL:  repo.URL,
	}, nil
}

func (a *adapter) CreateRegistryIfDoesntExist(registry string) (bool, error) {
	// Nexus repositories are typically created through the UI or API by administrators
	// This adapter assumes repositories already exist
	_, err := a.client.getRepository(registry)
	if err != nil {
		return false, fmt.Errorf("repository %s does not exist and cannot be created automatically", registry)
	}
	return false, nil // Repository already exists
}

func (a *adapter) GetPackages(registry string, artifactType types.ArtifactType, root *types.TreeNode) (
	[]types.Package,
	error,
) {
	var packages []types.Package

	l, err := a.listing(registry, nil)
	if err != nil {
		return nil, err
	}

	pkgNames := make(map[string][]string)
	assetToVersion := make(map[string]string)
	for _, component := range l.components {
		if artifactType == types.HELM_LEGACY {
			for _, asset := range component.Assets {
				if asset.Format == "helm" {
					if _, ok := pkgNames[component.Name]; !ok {
						pkgNames[component.Name] = []string{}
					}
					pkgNames[component.Name] = append(pkgNames[component.Name], asset.Path)
					assetToVersion[asset.Path] = component.Version
				}
			}
		} else {
			var pkgName string
			if component.Group != "" {
				pkgName = fmt.Sprintf("%s/%s", component.Group, component.Name)
			} else {
				pkgName = component.Name
			}
			pkgNames[pkgName] = []string{}
		}
	}

	for pkgName, urls := range pkgNames {
		path := ""
		if artifactType == types.HELM || artifactType == types.DOCKER {
			path = "v2/" + pkgName
		} else if artifactType == types.PYTHON {
			path = "packages/" + pkgName
		}

		if urls != nil && len(urls) > 0 {
			for _, url := range urls {
				pkg := types.Package{
					Registry: registry,
					Name:     pkgName,
					Path:     path,
					URL:      url,
					Version:  assetToVersion[url],
				}
				packages = append(packages, pkg)
			}
		} else {
			pkg := types.Package{
				Registry: registry,
				Name:     pkgName,
				Path:     path,
				URL:      "",
				Version:  "",
			}
			packages = append(packages, pkg)
		}

	}

	return packages, nil
}

func (a *adapter) addToTree(root *types.TreeNode, pkg types.Package, artifactType types.ArtifactType) {
	// Create tree structure based on package path
	pathParts := strings.Split(pkg.Path, "/")
	currentNode := root

	for i, part := range pathParts {
		if part == "" {
			continue
		}

		// Find or create child node
		var childNode *types.TreeNode
		for j := range currentNode.Children {
			if currentNode.Children[j].Name == part {
				childNode = &currentNode.Children[j]
				break
			}
		}

		if childNode == nil {
			newNode := types.TreeNode{
				Name:     part,
				Key:      strings.Join(pathParts[:i+1], "/"),
				Children: []types.TreeNode{},
				IsLeaf:   i == len(pathParts)-1,
			}
			currentNode.Children = append(currentNode.Children, newNode)
			childNode = &currentNode.Children[len(currentNode.Children)-1]
		}

		currentNode = childNode
	}
}

func (a *adapter) GetVersions(
	p types.Package,
	node *types.TreeNode, registry, pkg string, artifactType types.ArtifactType,
) ([]types.Version, error) {
	l, err := a.listing(registry, nil)
	if err != nil {
		return nil, err
	}

	var versions []types.Version
	if artifactType == types.MAVEN {
		for _, i := range l.byGroupName[pkg] {
			component := l.components[i]
			versions = append(versions, types.Version{
				Registry: registry,
				Pkg:      pkg,
				Name:     component.Version,
				Path:     mavenVersionTreePath(component.Group, component.Name, component.Version),
				Size:     componentSize(component),
			})
		}
		return versions, nil
	}

	for _, component := range l.components {
		if component.Name == pkg || strings.Contains(component.Name, pkg) {
			versions = append(versions, types.Version{
				Registry: registry,
				Pkg:      pkg,
				Name:     component.Version,
				Path:     component.Version,
				Size:     componentSize(component),
			})
		}
	}
	return versions, nil
}

func componentSize(component NexusComponent) int {
	total := 0
	for _, asset := range component.Assets {
		total += int(asset.FileSize)
	}
	return total
}

// Maven packages resolve to the tree root, so a version's path must be its full
// repository layout path or the version node lookup fails and nothing migrates.
func mavenVersionTreePath(group, artifact, version string) string {
	return strings.Join([]string{strings.ReplaceAll(group, ".", "/"), artifact, version}, "/")
}

func (a *adapter) GetFiles(registry string) ([]types.File, error) {
	return a.GetFilesWithProgress(registry, nil)
}

func (a *adapter) GetFilesWithProgress(registry string, progress func(string)) ([]types.File, error) {
	if err := a.requireHosted(registry); err != nil {
		return nil, err
	}
	l, err := a.listing(registry, progress)
	if err != nil {
		return nil, err
	}
	return append([]types.File(nil), l.files...), nil
}

func (a *adapter) requireHosted(registry string) error {
	repository, err := a.client.getRepository(registry)
	if err != nil {
		return fmt.Errorf("get repository: %w", err)
	}
	if repository.Type != "hosted" {
		return fmt.Errorf("repository %s is not a hosted repository", registry)
	}
	return nil
}

func (a *adapter) ListBatches(
	ctx context.Context,
	registry string,
	batchSize int,
	progress func(string),
	fn func(batch adp.Adapter, files []types.File) error,
) error {
	if err := a.requireHosted(registry); err != nil {
		return err
	}

	var pending []NexusComponent
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		l := newListing(registry, pending)
		pending = nil
		return fn(a.scopedTo(registry, l), l.files)
	}
	err := a.client.streamComponents(ctx, registry, progress, func(items []NexusComponent) error {
		pending = append(pending, items...)
		if len(pending) < batchSize {
			return nil
		}
		return flush()
	})
	if err != nil {
		return fmt.Errorf("failed to list components in registry %s: %w", registry, err)
	}
	return flush()
}

func (a *adapter) DownloadFile(registry string, uri string) (io.ReadCloser, http.Header, error) {
	downloadURL := a.client.buildDownloadURL(registry, uri)
	return a.client.getAsset(downloadURL)
}

func (a *adapter) GetOCIImagePath(registry string, packageHostname string, image string) (string, error) {
	if packageHostname != "" {
		return util.GenOCIImagePath(packageHostname, image), nil
	}

	port, err := a.client.getDockerPort(registry, a.reg.Insecure)
	if err != nil {
		return "", fmt.Errorf("failed to get Docker port (set sourcePackageHostname in the registry mapping "+
			"if the repository is served through a reverse proxy or subdomain): %w", err)
	}

	host, err := nexusDockerHost(a.reg.Endpoint, port)
	if err != nil {
		return "", fmt.Errorf("failed to get OCI host: %w", err)
	}
	return util.GenOCIImagePath(host, image), nil
}

func nexusDockerHost(endpoint string, port int) (string, error) {
	parsedURL, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("failed to parse endpoint URL: %w", err)
	}

	// Replace or add the port to the host
	host := parsedURL.Hostname()
	if host == "" {
		return "", fmt.Errorf("invalid endpoint URL: no hostname found")
	}

	// Create new host with the specified port
	newHost := fmt.Sprintf("%s:%d", host, port)

	return newHost, nil
}

func (a *adapter) UploadFile(
	registry string,
	file io.ReadCloser,
	f *types.File,
	header http.Header,
	artifactName string,
	version string,
	artifactType types.ArtifactType,
	metadata map[string]interface{},
) error {
	defer file.Close()
	// A cached listing of this repository no longer reflects what it contains.
	defer a.listings.invalidate(registry)

	// Construct the upload path based on artifact type and metadata
	uploadPath := a.constructUploadPath(artifactName, version, artifactType, metadata)

	return a.client.uploadAsset(registry, file, f.Name, uploadPath)
}

func (a *adapter) constructUploadPath(
	artifactName, version string,
	artifactType types.ArtifactType,
	metadata map[string]interface{},
) string {
	switch artifactType {
	case types.MAVEN:
		if group, ok := metadata["groupId"].(string); ok {
			groupPath := strings.ReplaceAll(group, ".", "/")
			return fmt.Sprintf("%s/%s/%s", groupPath, artifactName, version)
		}
	case types.NPM:
		if scope, ok := metadata["scope"].(string); ok && scope != "" {
			return fmt.Sprintf("%s/%s", scope, artifactName)
		}
	case types.PYTHON:
		return fmt.Sprintf("%s/%s", artifactName, version)
	case types.DOCKER:
		return fmt.Sprintf("%s/%s", artifactName, version)
	}

	// Default path
	return fmt.Sprintf("%s/%s", artifactName, version)
}

func (a *adapter) AddNPMTag(registry string, name string, version string, uri string) error {
	// Nexus V3 doesn't have a direct API for adding NPM tags
	// This would typically be handled through the NPM client or Nexus UI
	log.Warn().Msg("NPM tag addition not directly supported in Nexus V3 adapter")
	return nil
}

func (a *adapter) VersionExists(
	ctx context.Context,
	p types.Package,
	registry, pkg, version string,
	artifactType types.ArtifactType,
) (bool, error) {
	versions, err := a.GetVersions(p, nil, registry, pkg, artifactType)
	if err != nil {
		return false, fmt.Errorf("failed to get versions: %w", err)
	}

	for _, v := range versions {
		if v.Name == version {
			return true, nil
		}
	}

	return false, nil
}

func (a *adapter) UploadExists(_ context.Context, _ string, _ *types.File, _, _ string, _ types.ArtifactType,
	_ map[string]interface{}) (bool, error) {
	return false, fmt.Errorf("not implemented")
}

func (a *adapter) FileExists(
	ctx context.Context,
	registry, pkg, version string,
	fileName *types.File,
	artifactType types.ArtifactType,
) (bool, error) {
	files, err := a.GetFiles(registry)
	if err != nil {
		return false, fmt.Errorf("failed to get files: %w", err)
	}

	expectedPath := a.constructFilePath(pkg, version, fileName.Name, artifactType)

	for _, file := range files {
		if strings.Contains(file.Uri, expectedPath) || file.Name == fileName.Name {
			return true, nil
		}
	}

	return false, nil
}

func (a *adapter) GetAllFilesForVersion(
	ctx context.Context,
	registryRef, pkg, version string,
) ([]string, error) {
	return nil, fmt.Errorf("not implemented")
}

func (a *adapter) constructFilePath(pkg, version, fileName string, artifactType types.ArtifactType) string {
	switch artifactType {
	case types.MAVEN:
		return fmt.Sprintf("%s/%s/%s", pkg, version, fileName)
	case types.NPM:
		return fmt.Sprintf("%s/-/%s", pkg, fileName)
	case types.PYTHON:
		return fmt.Sprintf("%s/%s/%s", pkg, version, fileName)
	default:
		return fmt.Sprintf("%s/%s/%s", pkg, version, fileName)
	}
}

func (a *adapter) CreateVersion(
	registry string,
	artifactName string,
	version string,
	artifactType types.ArtifactType,
	files []*types.PackageFiles,
	metadata map[string]interface{},
) error {
	return nil
}

func (a *adapter) SearchFiles(_ string) ([]types.SearchedFile, error) {
	return nil, fmt.Errorf("date filter (SearchFiles) is not supported for this source adapter")
}
func (a *adapter) BuildExistingIndex(_ context.Context, _ string, _ int) (*types.ExistingIndex, error) {
	return nil, nil
}
