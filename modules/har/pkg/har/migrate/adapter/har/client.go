package har

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	http2 "net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/harness/cli/modules/har/pkg/har/migrate/adapter/har/arapi"
	"github.com/harness/cli/modules/har/pkg/har/migrate/adapter/har/arpkg"
	"github.com/harness/cli/modules/har/pkg/har/migrate/types"
	"github.com/harness/cli/modules/har/pkg/har/migrate/util"

	"github.com/google/uuid"
	retryablehttp "github.com/hashicorp/go-retryablehttp"
	"github.com/rs/zerolog/log"
)

type xAPIKeyTransport struct {
	base  http2.RoundTripper
	token string
}

func (t *xAPIKeyTransport) RoundTrip(req *http2.Request) (*http2.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("x-api-key", t.token)
	req.Header.Set("User-Agent", util.UserAgentString())
	return t.base.RoundTrip(req)
}

func retryingHTTPClient() *http2.Client {
	rc := retryablehttp.NewClient()
	rc.RetryMax = 5
	rc.RetryWaitMin = 200 * time.Millisecond
	rc.RetryWaitMax = 1 * time.Minute
	rc.Backoff = retryablehttp.RateLimitLinearJitterBackoff
	rc.Logger = nil

	std := rc.StandardClient() // returns *http.Client using a retrying RoundTripper
	std.Timeout = 20 * time.Second
	return std
}

// newClient constructs a har client
func newClient(reg *types.RegistryConfig) *client {
	username := reg.Credentials.Username
	token := reg.Credentials.Password

	withXApiKey := func(c *arapi.Client) error {
		c.RequestEditors = append(c.RequestEditors, func(ctx context.Context, req *http2.Request) error {
			req.Header.Set("x-api-key", token)
			req.Header.Set("User-Agent", util.UserAgentString())
			return nil
		})
		return nil
	}
	withXApiKeyPkg := func(c *arpkg.Client) error {
		c.RequestEditors = append(c.RequestEditors, func(ctx context.Context, req *http2.Request) error {
			req.Header.Set("x-api-key", token)
			req.Header.Set("User-Agent", util.UserAgentString())
			return nil
		})
		return nil
	}

	arClient, _ := arapi.NewClientWithResponses(reg.APIBaseURL+"/gateway/har/api/v1",
		arapi.WithHTTPClient(retryingHTTPClient()),
		withXApiKey)

	pkgClient, _ := arpkg.NewClientWithResponses(reg.Endpoint, withXApiKeyPkg)

	return &client{
		client: &http2.Client{
			Transport: &xAPIKeyTransport{
				base:  &http2.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
				token: token,
			},
		},
		pkgClient: pkgClient,
		url:       reg.Endpoint,
		insecure:  true,
		username:  username,
		password:  token,
		apiClient: arClient,
		accountID: reg.AccountID,
	}
}

type client struct {
	apiClient *arapi.ClientWithResponses
	client    *http2.Client
	url       string
	insecure  bool
	username  string
	password  string
	pkgClient *arpkg.ClientWithResponses
	accountID string
}

func (c *client) uploadGenericFile(registry, artifactName, version string, f *types.File, file io.ReadCloser) error {
	// For generic, include package/version as path segments: {package}/{version}/{filepath}
	fileUri := strings.TrimPrefix(f.Uri, "/")
	fullPath := fmt.Sprintf("%s/%s/%s", artifactName, version, fileUri)
	defer file.Close()

	_, err2 := c.pkgClient.UploadGenericFileToPathWithBodyWithResponse(
		context.Background(),
		c.accountID,
		registry,
		fullPath,
		"application/octet-stream",
		file)

	if err2 != nil {
		return fmt.Errorf("failed to upload file '%s/%s': %w", artifactName, version, err2)
	}

	return nil
}

func (c *client) headRawFile(registryRef string, fileUri string) (bool, error) {
	fileUri = strings.TrimPrefix(fileUri, "/")
	parts := strings.Split(registryRef, "/")
	registry := parts[len(parts)-1]
	resp, err := c.pkgClient.HeadGenericFileAtPathWithResponse(
		context.Background(),
		c.accountID,
		registry,
		fileUri,
	)
	if err != nil {
		return false, fmt.Errorf("failed to HEAD raw file '%s': %w", fileUri, err)
	}

	if resp.StatusCode() == http2.StatusOK {
		return true, nil
	}
	if resp.StatusCode() == http2.StatusNotFound {
		return false, nil
	}
	return false, fmt.Errorf("unexpected status code %d for HEAD on raw file '%s'", resp.StatusCode(), fileUri)
}

func (c *client) uploadRawFile(registry string, f *types.File, file io.ReadCloser) error {
	fileUri := strings.TrimPrefix(f.Uri, "/")
	defer file.Close()

	resp, err := c.pkgClient.UploadGenericFileToPathWithBodyWithResponse(
		context.Background(),
		c.accountID,
		registry,
		fileUri,
		"application/octet-stream",
		file,
	)
	if err != nil {
		return fmt.Errorf("failed to upload raw file '%s': %w", fileUri, err)
	}
	if resp.StatusCode() == http2.StatusConflict {
		return types.ErrArtifactAlreadyExists
	}
	if resp.StatusCode() < 200 || resp.StatusCode() > 299 {
		return fmt.Errorf("failed to upload raw file '%s', status %d", fileUri, resp.StatusCode())
	}

	return nil
}

func (c *client) uploadMavenFile(
	registry string,
	name string,
	version string,
	f *types.File,
	file io.ReadCloser,
) error {
	fileUri := strings.TrimPrefix(f.Uri, "/")
	url := fmt.Sprintf("%s/maven/%s/%s/%s", c.url, c.accountID, registry, fileUri)
	// Create request
	req, err := http2.NewRequest(http2.MethodPut, url, file)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to upload file '%s': %w", fileUri, err)
	}
	defer resp.Body.Close()

	// Check for successful response
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to upload file '%s', status code: %d, response: %s",
			fileUri, resp.StatusCode, string(body))
	}

	return nil
}

func (c *client) uploadNugetFile(
	registry string,
	name string,
	version string,
	f *types.File,
	file io.ReadCloser,
) error {
	fileUri := strings.TrimPrefix(f.Uri, "/")

	subDir := nugetSubDir(fileUri)

	url := fmt.Sprintf("%s/pkg/%s/%s/nuget/%s", c.url, c.accountID, registry, subDir)
	if strings.HasSuffix(f.Name, ".snupkg") {
		url = fmt.Sprintf("%s/pkg/%s/%s/nuget/symbolpackage/%s", c.url, c.accountID, registry, subDir)
	}

	// Create a pipe to write the multipart form data
	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)

	// Process multipart form asynchronously
	go func() {
		defer pw.Close()
		defer writer.Close()

		// Add the file as "content" field
		part, err := writer.CreateFormFile("package", f.Name)
		if err != nil {
			pw.CloseWithError(err)
			return
		}

		// Copy the file content
		if _, err := io.Copy(part, file); err != nil {
			pw.CloseWithError(err)
			return
		}
	}()

	// Create request
	req, err := http2.NewRequest(http2.MethodPut, url, pr)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", writer.FormDataContentType())

	// Execute request with our client (which handles authentication)
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to upload file '%s': %w", fileUri, err)
	}
	defer resp.Body.Close()

	// Check for successful response
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to upload file '%s', status code: %d, response: %s",
			fileUri, resp.StatusCode, string(body))
	}

	return nil
}

// nugetSubDir extracts the subdirectory prefix from a NuGet file URI.
// JFrog stores NuGet packages as: /{subdir}/{packageId}/{version}/{filename}
// The last 3 segments are always packageId/version/filename.
// Returns the subdirectory with a trailing slash, or empty string if none.
//
// Examples:
//
//	"foo/company.grpc.pkg/1.0.0/company.grpc.pkg.1.0.0.nupkg" → "foo/"
//	"a/b/pkg/1.0.0/pkg.1.0.0.nupkg"                           → "a/b/"
//	"company.grpc.pkg/1.0.0/company.grpc.pkg.1.0.0.nupkg"     → ""
func nugetSubDir(fileUri string) string {
	parts := strings.Split(strings.TrimPrefix(fileUri, "/"), "/")
	// Need at least 4 segments for there to be a subdirectory
	// (subdir + packageId + version + filename)
	if len(parts) <= 3 {
		return ""
	}
	return strings.Join(parts[:len(parts)-3], "/") + "/"
}

func (c *client) uploadNPMFile(
	registry string,
	name string,
	version string,
	f *types.File,
	file io.ReadCloser,
) error {
	url := fmt.Sprintf("%s/pkg/%s/%s/npm/%s", c.url, c.accountID, registry, name)

	// Create request
	req, err := http2.NewRequest(http2.MethodPut, url, file)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to upload file '%s': %w", url, err)
	}
	defer resp.Body.Close()

	// Check for successful response
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to upload file '%s', status code: %d, response: %s",
			url, resp.StatusCode, string(body))
	}

	return nil
}

func (c *client) uploadRPMFile(
	registry string,
	filename string,
	file io.ReadCloser,
) error {
	fileUri := strings.TrimPrefix(filename, "/")
	url := fmt.Sprintf("%s/pkg/%s/%s/rpm/%s", c.url, c.accountID, registry, fileUri)

	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)

	// Process multipart form asynchronously
	go func() {
		defer pw.Close()
		defer writer.Close()

		part, err := writer.CreateFormFile("file", filename)
		if err != nil {
			pw.CloseWithError(err)
			return
		}

		if _, err := io.Copy(part, file); err != nil {
			pw.CloseWithError(err)
			return
		}
	}()

	req, err := http2.NewRequest(http2.MethodPut, url, pr)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to upload file '%s': %w", fileUri, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to upload file '%s', status code: %d, response: %s",
			fileUri, resp.StatusCode, string(body))
	}

	return nil
}

func (c *client) uploadCondaFile(
	registry string,
	filename string,
	file io.ReadCloser,
	metadata map[string]interface{},
) error {
	fileUri := strings.TrimPrefix(filename, "/")
	url := fmt.Sprintf("%s/pkg/%s/%s/conda/upload", c.url, c.accountID, registry)
	// Create request
	req, err := http2.NewRequest(http2.MethodPut, url, file)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/octet-stream")
	// Add headers from metadata
	for key, val := range metadata {
		switch v := val.(type) {
		case string:
			if v != "" {
				req.Header.Set(key, v)
			}
		}
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to upload file '%s': %w", fileUri, err)
	}
	defer resp.Body.Close()

	// Check for successful response
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to upload file '%s', status code: %d, response: %s",
			fileUri, resp.StatusCode, string(body))
	}
	return nil
}

func (c *client) uploadComposerFile(
	registry string,
	filename string,
	file io.ReadCloser,
) error {
	url := fmt.Sprintf("%s/pkg/%s/%s/composer/upload", c.url, c.accountID, registry)

	// Create request
	req, err := http2.NewRequest(http2.MethodPost, url, file)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to upload file '%s': %w", filename, err)
	}
	defer resp.Body.Close()

	// Check for successful response
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to upload file '%s', status code: %d, response: %s",
			filename, resp.StatusCode, string(body))
	}
	return nil
}

func (c *client) uploadSwiftFile(
	registry string,
	filename string,
	file io.ReadCloser,
	packageName string,
	version string,
) error {

	// Parse package name to extract scope, name
	// packageName is in scope.name format (e.g., "myscope.harness")
	parts := strings.SplitN(packageName, ".", 2)
	if len(parts) < 2 {
		return fmt.Errorf("invalid Swift package name format: %s, expected format: scope.name", packageName)
	}

	scope := parts[0]
	name := parts[1]

	url := fmt.Sprintf("%s/pkg/%s/%s/swift/%s/%s/%s",
		c.url, c.accountID, registry, scope, name, version)

	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)

	go func() {
		defer pw.Close()
		defer writer.Close()

		// Create the form field "source-archive" to match Swift upload API
		part, err := writer.CreateFormFile("source-archive", filename)
		if err != nil {
			pw.CloseWithError(err)
			return
		}

		// Copy the file into the multipart field
		if _, err := io.Copy(part, file); err != nil {
			pw.CloseWithError(err)
			return
		}
	}()

	req, err := http2.NewRequest(http2.MethodPut, url, pr)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Accept", "application/vnd.swift.registry.v1+json")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to upload Swift file '%s': %w", filename, err)
	}
	defer resp.Body.Close()

	// Check for successful response
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to upload Swift file '%s', status code: %d, response: %s",
			filename, resp.StatusCode, string(body))
	}
	return nil
}

func (c *client) AddNPMTag(registry string, name string, version string, tagUri string) error {
	url := fmt.Sprintf("%s/pkg/%s/%s/npm", c.url, c.accountID, registry)
	url = url + tagUri
	versionJSON, err := json.Marshal(version)
	if err != nil {
		return fmt.Errorf("failed to marshal version to json: %w", err)
	}

	req, err := http2.NewRequest(http2.MethodPut, url, bytes.NewReader(versionJSON))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to upload file '%s': %w", url, err)
	}
	defer resp.Body.Close()

	// Check for successful response
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to upload file '%s', status code: %d, response: %s",
			url, resp.StatusCode, string(body))
	}

	return nil
}

func (c *client) uploadDartFile(
	registry string,
	name string,
	version string,
	f *types.File,
	file io.ReadCloser,
) error {
	// POST {endpoint}/pkg/{account_id}/{registry}/pub/api/packages/versions/new/upload/{upload_id}
	// with multipart form: -F "file=@file.tar.gz"
	uploadID := uuid.New().String()

	base := strings.TrimRight(c.url, "/")
	url := fmt.Sprintf("%s/pkg/%s/%s/pub/api/packages/versions/new/upload/%s", base, c.accountID, registry,
		uploadID)

	// Create a pipe for streaming multipart form data
	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)

	// Write multipart form in a goroutine
	go func() {
		defer pw.Close()
		defer writer.Close()

		// Create form file field "file"
		part, err := writer.CreateFormFile("file", f.Name)
		if err != nil {
			pw.CloseWithError(fmt.Errorf("failed to create form file: %w", err))
			return
		}

		// Copy file content to the form field
		if _, err := io.Copy(part, file); err != nil {
			pw.CloseWithError(fmt.Errorf("failed to write file to form: %w", err))
			return
		}
	}()

	req, err := http2.NewRequest(http2.MethodPost, url, pr)
	if err != nil {
		return fmt.Errorf("failed to create Dart upload request: %w", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to upload Dart package to '%s': %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to upload Dart package '%s', status code: %d, response: %s",
			url, resp.StatusCode, string(body))
	}

	return nil
}

func (c *client) uploadPythonFile(
	registry string,
	name string,
	version string,
	f *types.File,
	file io.ReadCloser,
	metadata map[string]interface{},
) error {
	fileUri := strings.TrimPrefix(f.Uri, "/")
	url := fmt.Sprintf("%s/pkg/%s/%s/python/%s", c.url, c.accountID, registry, fileUri)

	// Create a pipe to write the multipart form data
	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)

	// Process multipart form asynchronously
	go func() {
		defer pw.Close()
		defer writer.Close()

		// Add the file as "content" field
		part, err := writer.CreateFormFile("content", f.Name)
		if err != nil {
			pw.CloseWithError(err)
			return
		}

		// Copy the file content
		if _, err := io.Copy(part, file); err != nil {
			pw.CloseWithError(err)
			return
		}

		// Add metadata fields
		if metadata != nil {
			// Iterate through all metadata and add as form fields
			for key, val := range metadata {
				switch v := val.(type) {
				case []string:
					// Handle array values
					for _, item := range v {
						if err := writer.WriteField(key, item); err != nil {
							pw.CloseWithError(err)
							return
						}
					}
				default:
					// Handle simple values
					if val != nil && fmt.Sprintf("%v", val) != "" {
						if err := writer.WriteField(key, fmt.Sprintf("%v", val)); err != nil {
							pw.CloseWithError(err)
							return
						}
					}
				}
			}
		}
	}()

	// Create request
	req, err := http2.NewRequest(http2.MethodPost, url, pr)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", writer.FormDataContentType())

	// Execute request with our client (which handles authentication)
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to upload file '%s': %w", fileUri, err)
	}
	defer resp.Body.Close()

	// Check for successful response
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to upload file '%s', status code: %d, response: %s",
			fileUri, resp.StatusCode, string(body))
	}

	return nil
}

func (c *client) artifactFileExists(
	ctx context.Context,
	registryRef, pkg, version string,
	file *types.File,
	artifactType types.ArtifactType,
) (bool, error) {
	page := int64(0)
	size := int64(100)
	fileURI := file.Name
	if artifactType == types.GENERIC || artifactType == types.RAW {
		fileURI = strings.TrimPrefix(file.Uri, "/")
	} else {
		fileURI = strings.TrimPrefix(fileURI, "/")
	}

	for {
		response, err := c.apiClient.GetArtifactFilesWithResponse(ctx, registryRef, pkg, version,
			&arapi.GetArtifactFilesParams{
				Page:      &page,
				Size:      &size,
				SortOrder: nil,
				SortField: nil,
			})
		if err != nil {
			return false, fmt.Errorf("failed to get artifact files: %w", err)
		}
		if response.StatusCode() != http2.StatusOK {
			return false, fmt.Errorf("failed to get artifact files: %s", response.Status())
		}
		data := response.JSON200
		for _, v := range data.Data.Files {
			if v.Name == fileURI {
				return true, nil
			}
		}
		if len(data.Data.Files) < int(size) || (nil != data.Data.PageCount && nil != data.Data.PageIndex && (*data.Data.PageIndex+1 >= *data.Data.PageCount)) {
			break
		}
		page++
	}
	return false, nil
}

func (c *client) artifactGetFilesForVersion(
	ctx context.Context,
	registryRef, pkg, version string,
) ([]string, error) {
	page := int64(0)
	size := int64(100)

	var allFileNames []string
	for {
		response, err := c.apiClient.GetArtifactFilesWithResponse(ctx, registryRef, pkg, version,
			&arapi.GetArtifactFilesParams{
				Page:      &page,
				Size:      &size,
				SortOrder: nil,
				SortField: nil,
			})
		if err != nil {
			return allFileNames, fmt.Errorf("failed to get artifact files: %w", err)
		}
		if response.StatusCode() != http2.StatusOK {
			return allFileNames, fmt.Errorf("failed to get artifact files: %s", response.Status())
		}
		data := response.JSON200

		for _, v := range data.Data.Files {
			allFileNames = append(allFileNames, v.Name)
		}
		if len(data.Data.Files) < int(size) || (nil != data.Data.PageCount && nil != data.Data.PageIndex && (*data.Data.PageIndex+1 >= *data.Data.PageCount)) {
			break
		}
		page++
	}
	return allFileNames, nil
}

func (c *client) getRegistry(
	ctx context.Context,
	registry string,
) (types.RegistryInfo, error) {
	page := int64(0)
	size := int64(100)
	for {
		descendants := arapi.GetAllRegistriesParamsScopeDescendants
		response, err := c.apiClient.GetAllRegistriesWithResponse(ctx, c.accountID,
			&arapi.GetAllRegistriesParams{
				Page:       &page,
				Size:       &size,
				SearchTerm: &registry,
				Scope:      &descendants,
			})
		if err != nil || response.StatusCode() != http2.StatusOK {
			return types.RegistryInfo{}, fmt.Errorf("failed to get registry: %w", err)
		}
		data := response.JSON200
		if data == nil {
			return types.RegistryInfo{}, fmt.Errorf("failed to get data for registry: %s", response.Status())
		}
		registries := data.Data.Registries

		for _, v := range registries {
			if v.Identifier != registry {
				continue
			}
			return types.RegistryInfo{
				Type: string(v.Type),
				URL:  v.Url,
				Path: *v.Path,
			}, nil
		}
		if len(registries) < int(size) || (nil != data.Data.PageCount && nil != data.Data.PageIndex && (*data.Data.PageIndex+1 >= *data.Data.PageCount)) {
			break
		}
		page++
	}
	return types.RegistryInfo{}, fmt.Errorf("failed to find registry '%s'", registry)
}

func (c *client) artifactVersionExists(
	ctx context.Context,
	registryRef, pkg, version string,
	artifactType types.ArtifactType,
) (bool, error) {
	page := int64(0)
	size := int64(100)

	for {
		response, err := c.apiClient.GetAllArtifactVersionsWithResponse(ctx, registryRef, pkg,
			&arapi.GetAllArtifactVersionsParams{
				Page:       &page,
				Size:       &size,
				SortOrder:  nil,
				SortField:  nil,
				SearchTerm: &version,
			})
		if err != nil {
			return false, fmt.Errorf("failed to get artifact versions: %w", err)
		}
		if response.StatusCode() == http2.StatusNotFound {
			return false, nil
		}
		if response.StatusCode() != http2.StatusOK {
			return false, fmt.Errorf("failed to get artifact versions: %s", response.Status())
		}
		var data arapi.ListArtifactVersion

		if response.JSON200 == nil {
			return false, fmt.Errorf("failed to get artifact 200 response: %s", response.Status())
		}
		data = response.JSON200.Data
		if data.ArtifactVersions == nil {
			return false, nil
		}

		for _, v := range *data.ArtifactVersions {
			if v.Name == version {
				return true, nil
			}
		}
		if len(*data.ArtifactVersions) < int(size) || (nil != data.PageCount && nil != data.PageIndex && (*data.PageIndex+1 >= *data.PageCount)) {
			break
		}
		page++
	}
	return false, nil
}

func (c *client) createGoVersion(
	registry string,
	artifactName string,
	version string,
	files []*types.PackageFiles,
) error {
	// Create a pipe to write the file contents
	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)

	// Process multipart form asynchronously
	go func() {
		defer pw.Close()
		for _, file := range files {
			// Add the file
			extension := filepath.Ext(file.File.Name)
			formFieldName := strings.TrimPrefix(extension, ".")
			part, err := writer.CreateFormFile(formFieldName, file.File.Name)
			if err != nil {
				pw.CloseWithError(err)
				return
			}

			// Copy the file content
			if _, err := io.Copy(part, file.DownloadFile); err != nil {
				pw.CloseWithError(err)
				return
			}
		}
		// Close the writer
		if err := writer.Close(); err != nil {
			pw.CloseWithError(err)
			return
		}
	}()

	url := fmt.Sprintf("%s/pkg/%s/%s/go/upload", c.url, c.accountID, registry)
	// Create request
	req, err := http2.NewRequest(http2.MethodPut, url, pr)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", writer.FormDataContentType())

	// Execute request with our client (which handles authentication)
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to upload file '%s/%s': %w", artifactName, version, err)
	}
	defer resp.Body.Close()

	// Check for successful response
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to upload file '%s/%s', status code: %d, response: %s",
			artifactName, version, resp.StatusCode, string(body))
	}

	return nil
}

func (c *client) uploadDebianFile(
	registry string,
	f *types.File,
	file io.ReadCloser,
	metadata map[string]interface{},
) error {
	distribution, _ := metadata["distribution"].(string)
	component, _ := metadata["component"].(string)
	fileType, _ := metadata["fileType"].(string)
	packageName, _ := metadata["packageName"].(string)
	version, _ := metadata["version"].(string)

	if distribution == "" {
		return fmt.Errorf("distribution is required in metadata for debian upload")
	}
	if component == "" {
		return fmt.Errorf("component is required in metadata for debian upload")
	}
	if fileType == "" {
		fileType = "deb"
	}
	if fileType == "src" {
		if packageName == "" {
			return fmt.Errorf("packageName is required in metadata for debian src upload")
		}
		if version == "" {
			return fmt.Errorf("version is required in metadata for debian src upload")
		}
	}

	endpoint := fileType // "deb", "dsc", or "src"
	baseURL := fmt.Sprintf("%s/pkg/%s/%s/debian/%s?distribution=%s&component=%s",
		c.url, c.accountID, registry, endpoint,
		strings.ReplaceAll(distribution, " ", "%20"),
		strings.ReplaceAll(component, " ", "%20"),
	)
	if fileType == "src" {
		baseURL += "&package=" + packageName + "&version=" + version
	}

	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)
	go func() {
		defer pw.Close()
		defer writer.Close()
		part, err := writer.CreateFormFile("file", f.Name)
		if err != nil {
			pw.CloseWithError(err)
			return
		}
		if _, err := io.Copy(part, file); err != nil {
			pw.CloseWithError(err)
		}
	}()

	req, err := http2.NewRequest(http2.MethodPut, baseURL, pr)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to upload debian file '%s': %w", f.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to upload debian file '%s', status %d: %s", f.Name, resp.StatusCode, string(body))
	}
	return nil
}

func (c *client) uploadPuppetFile(
	registry string,
	f *types.File,
	file io.ReadCloser,
) error {
	uploadURL := fmt.Sprintf("%s/pkg/%s/%s/puppet/upload", c.url, c.accountID, registry)

	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)
	go func() {
		defer pw.Close()
		defer writer.Close()
		part, err := writer.CreateFormFile("file", f.Name)
		if err != nil {
			pw.CloseWithError(err)
			return
		}
		if _, err := io.Copy(part, file); err != nil {
			pw.CloseWithError(err)
		}
	}()

	req, err := http2.NewRequest(http2.MethodPut, uploadURL, pr)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to upload puppet file '%s': %w", f.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to upload puppet file '%s', status %d: %s", f.Name, resp.StatusCode, string(body))
	}
	return nil
}

func (c *client) uploadConanFile(
	registry string,
	file io.ReadCloser,
	metadata map[string]interface{},
) error {
	defer file.Close()

	name, _ := metadata["name"].(string)
	version, _ := metadata["version"].(string)
	rrev, _ := metadata["rrev"].(string)
	filename, _ := metadata["filename"].(string)
	user, _ := metadata["user"].(string)
	channel, _ := metadata["channel"].(string)
	sha1, _ := metadata["sha1"].(string)
	layer, _ := metadata["layer"].(string)
	pkgid, _ := metadata["pkgid"].(string)
	prev, _ := metadata["prev"].(string)

	if name == "" || version == "" || rrev == "" || filename == "" {
		return fmt.Errorf("uploadConanFile: missing required metadata fields (name=%q, version=%q, rrev=%q, filename=%q)",
			name, version, rrev, filename)
	}
	if layer == "package" && (pkgid == "" || prev == "") {
		return fmt.Errorf("uploadConanFile: package layer requires pkgid and prev (pkgid=%q, prev=%q)", pkgid, prev)
	}

	if user == "" {
		user = "_"
	}
	if channel == "" {
		channel = "_"
	}

	var uploadURL string
	if layer == "package" {
		uploadURL = fmt.Sprintf("%s/pkg/%s/%s/conan/v2/conans/%s/%s/%s/%s/revisions/%s/packages/%s/revisions/%s/files/%s",
			c.url, c.accountID, registry, name, version, user, channel, rrev, pkgid, prev, filename)
	} else {
		uploadURL = fmt.Sprintf("%s/pkg/%s/%s/conan/v2/conans/%s/%s/%s/%s/revisions/%s/files/%s",
			c.url, c.accountID, registry, name, version, user, channel, rrev, filename)
	}

	req, err := http2.NewRequest(http2.MethodPut, uploadURL, file)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	if sha1 != "" {
		req.Header.Set("X-Checksum-Sha1", sha1)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to upload conan file '%s': %w", filename, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http2.StatusConflict {
		return types.ErrArtifactAlreadyExists
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to upload conan file '%s', status %d: %s", filename, resp.StatusCode, string(body))
	}
	return nil
}

func (c *client) buildExistingIndex(ctx context.Context, registryRef string, concurrency int) (*types.ExistingIndex, error) {
	// Step 1: collect all artifact (package) names for this registry.
	page := int64(0)
	size := int64(100)
	var artifactNames []string
	for {
		resp, err := c.apiClient.GetAllArtifactsByRegistryWithResponse(ctx, registryRef,
			&arapi.GetAllArtifactsByRegistryParams{Page: &page, Size: &size})
		if err != nil {
			return nil, fmt.Errorf("failed to list artifacts for index: %w", err)
		}
		if resp.StatusCode() != http2.StatusOK || resp.JSON200 == nil {
			return nil, fmt.Errorf("failed to list artifacts for index: %s", resp.Status())
		}
		for _, a := range resp.JSON200.Data.Artifacts {
			artifactNames = append(artifactNames, a.Name)
		}
		data := resp.JSON200.Data
		if len(data.Artifacts) < int(size) ||
			(data.PageCount != nil && data.PageIndex != nil && *data.PageIndex+1 >= *data.PageCount) {
			break
		}
		page++
	}

	// Step 2: for each artifact, collect all (pkg, version) pairs.
	type pkgVersion struct{ pkg, version string }
	var pvPairs []pkgVersion
	for _, name := range artifactNames {
		p := int64(0)
		for {
			resp, err := c.apiClient.GetAllArtifactVersionsWithResponse(ctx, registryRef, name,
				&arapi.GetAllArtifactVersionsParams{Page: &p, Size: &size})
			if err != nil {
				log.Warn().Err(err).Str("artifact", name).Msg("buildExistingIndex: failed to list versions, skipping artifact")
				break
			}
			if resp.StatusCode() != http2.StatusOK || resp.JSON200 == nil {
				log.Warn().Str("artifact", name).Str("status", resp.Status()).Msg("buildExistingIndex: unexpected status listing versions, skipping artifact")
				break
			}
			if resp.JSON200.Data.ArtifactVersions != nil {
				for _, v := range *resp.JSON200.Data.ArtifactVersions {
					if v.FileCount != nil && *v.FileCount == 0 {
						continue
					}
					pvPairs = append(pvPairs, pkgVersion{name, v.Name})
				}
			}
			data := resp.JSON200.Data
			if data.ArtifactVersions == nil || len(*data.ArtifactVersions) < int(size) ||
				(data.PageCount != nil && data.PageIndex != nil && *data.PageIndex+1 >= *data.PageCount) {
				break
			}
			p++
		}
	}

	// Step 3: fetch files per (pkg, version) concurrently, bounded by concurrency.
	idx := types.NewExistingIndex()
	if len(pvPairs) == 0 {
		return idx, nil
	}

	limit := concurrency
	if limit <= 0 {
		limit = 4
	}
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for _, pv := range pvPairs {
		pv := pv
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			names, err := c.artifactGetFilesForVersion(ctx, registryRef, pv.pkg, pv.version)
			if err != nil {
				log.Warn().Err(err).Str("pkg", pv.pkg).Str("version", pv.version).Msg("buildExistingIndex: failed to list files for version, skipping")
				return
			}
			for _, name := range names {
				idx.AddFile(pv.pkg, pv.version, name)
			}
		}()
	}
	wg.Wait()

	return idx, nil
}
