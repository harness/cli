package jfrog

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/harness/cli/modules/har/pkg/har/migrate/types"
)

type bearerTransport struct {
	base  http.RoundTripper
	token string
}

func (t *bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(req)
}

// parseNextLink extracts the URL of the rel="next" link from an HTTP Link header.
func parseNextLink(header string) string {
	for part := range strings.SplitSeq(header, ",") {
		var url, rel string
		for attr := range strings.SplitSeq(part, ";") {
			attr = strings.TrimSpace(attr)
			if len(attr) > 2 && attr[0] == '<' && attr[len(attr)-1] == '>' {
				url = attr[1 : len(attr)-1]
			} else if strings.HasPrefix(attr, "rel=") {
				rel = strings.Trim(attr[4:], `"`)
			}
		}
		if rel == "next" {
			return url
		}
	}
	return ""
}

// Client defines the interface for interacting with a JFrog Artifactory instance.
// It is implemented by the real HTTP client and can be replaced with a mock for testing.
type Client interface {
	GetRegistries() ([]JFrogRepository, error)
	GetRegistry(registry string) (JFrogRepository, error)
	GetFile(registry string, path string) (io.ReadCloser, http.Header, error)
	GetFiles(registry string) ([]types.File, error)
	GetCatalog(registry string) ([]string, error)
}

// newClient constructs a jfrog client
func newClient(reg *types.RegistryConfig) *client {
	return &client{
		client: &http.Client{
			Transport: &bearerTransport{
				base:  &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
				token: reg.Credentials.Password,
			},
		},
		url:      reg.Endpoint,
		insecure: true,
		username: reg.Credentials.Username,
		password: reg.Credentials.Password,
	}
}

type client struct {
	client   *http.Client
	url      string
	insecure bool
	username string
	password string
}

// JFrogPackage represents a file entry from JFrog Artifactory
type JFrogPackage struct {
	Registry string
	Path     string
	Name     string
	Size     int
}

type JFrogRepository struct {
	Key         string `json:"key"`
	Type        string `json:"type"`
	Url         string `json:"url"`
	Description string `json:"description"`
	PackageType string `json:"packageType"`
}

func (c *client) GetRegistries() ([]JFrogRepository, error) {
	url := fmt.Sprintf("%s/artifactory/api/repositories", c.url)

	resp, err := c.client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to get repositories: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("failed to get repositories: status %d", resp.StatusCode)
	}
	var repositories []JFrogRepository
	if err := json.NewDecoder(resp.Body).Decode(&repositories); err != nil {
		return nil, fmt.Errorf("failed to decode repositories: %w", err)
	}
	return repositories, nil
}

func (c *client) GetRegistry(registry string) (JFrogRepository, error) {
	repositories, err := c.GetRegistries()
	if err != nil {
		return JFrogRepository{}, fmt.Errorf("failed to get repositories: %w", err)
	}

	for _, repo := range repositories {
		if repo.Key == registry {
			if repo.Type != "LOCAL" {
				return JFrogRepository{}, fmt.Errorf(
					"registry %s is of type %s; only LOCAL repositories are supported for migration",
					registry, repo.Type)
			}
			return repo, nil
		}
	}

	return JFrogRepository{}, fmt.Errorf("registry %s not found", registry)
}

func (c *client) GetFile(registry string, path string) (io.ReadCloser, http.Header, error) {
	path = strings.TrimPrefix(path, "/")
	path = strings.TrimSuffix(path, "/")

	var url string
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		url = path
	} else {
		url = fmt.Sprintf("%s/artifactory/%s/%s", c.url, registry, path)
	}

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create request for file '%s': %w", path, err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to download file '%s': %w", path, err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, nil, fmt.Errorf("failed to download file '%s', status code: %d", path, resp.StatusCode)
	}

	return resp.Body, resp.Header, nil
}

// getFiles retrieves a list of files from the specified JFrog Artifactory registry
func (c *client) GetFiles(registry string) ([]types.File, error) {
	repo, err := c.GetRegistry(registry)
	if err != nil {
		return nil, fmt.Errorf("failed to get registry %s: %w", registry, err)
	}
	if repo.Type == "VIRTUAL" {
		return nil, fmt.Errorf("registry %s is a virtual repository", registry)
	}

	// Make GET request to fetch files
	url := fmt.Sprintf("%s/artifactory/api/storage/%s?list&deep=1", c.url, registry)

	// Define response structure for file list
	type fileListResponse struct {
		Files []struct {
			Uri          string `json:"uri"`
			Folder       bool   `json:"folder"`
			Size         int    `json:"size,omitempty"`
			LastModified string `json:"lastModified,omitempty"`
			SHA1         string `json:"sha1,omitempty"`
			SHA2         string `json:"sha2,omitempty"`
		} `json:"files"`
	}

	resp, err := c.client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to get files from registry '%s': %w", registry, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("failed to get files from registry '%s': status %d", registry, resp.StatusCode)
	}
	var fileList fileListResponse
	if err := json.NewDecoder(resp.Body).Decode(&fileList); err != nil {
		return nil, fmt.Errorf("failed to decode file list for registry '%s': %w", registry, err)
	}

	// Convert response to JFrogPackage slice
	var result []types.File
	for _, file := range fileList.Files {
		// Skip folders
		if file.Folder {
			continue
		}

		f := types.File{
			Registry:     registry,
			Name:         getFileName(file.Uri),
			Uri:          file.Uri,
			Folder:       file.Folder,
			Size:         file.Size,
			LastModified: file.LastModified,
			SHA1:         file.SHA1,
			SHA2:         file.SHA2,
		}

		result = append(result, f)
	}

	return result, nil
}

func getFileName(uri string) string {
	// Normalize the URI by removing any leading/trailing slashes
	uri = strings.TrimPrefix(uri, "/")
	uri = strings.TrimSuffix(uri, "/")

	// Handle empty URI
	if uri == "" {
		return ""
	}

	// Split the URI by path separator
	parts := strings.Split(uri, "/")

	// Return the last part, which should be the filename
	return parts[len(parts)-1]
}

func buildCatalogURL(endpoint, repo string) string {
	return fmt.Sprintf("%s/artifactory/api/docker/%s/v2/_catalog?n=1000", endpoint, repo)
}

func buildCatalogURLRelative(endpoint, repo, relativePath string) string {
	return fmt.Sprintf("%s/artifactory/api/docker/%s%s", endpoint, repo, relativePath)
}

func (c *client) GetCatalog(registry string) (repositories []string, err error) {
	url := buildCatalogURL(c.url, registry)
	for {
		repos, next, err := c.catalog(url)
		if err != nil {
			return nil, err
		}
		repositories = append(repositories, repos...)

		url = next
		// no next page, end the loop
		if len(url) == 0 {
			break
		}
		// relative URL
		if !strings.Contains(url, "://") {
			url = buildCatalogURLRelative(c.url, registry, url)
		}
	}
	return repositories, nil
}

func (c *client) catalog(url string) ([]string, string, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	repositories := struct {
		Repositories []string `json:"repositories"`
	}{}
	if err := json.Unmarshal(body, &repositories); err != nil {
		return nil, "", err
	}
	return repositories.Repositories, parseNextLink(resp.Header.Get("Link")), nil
}
