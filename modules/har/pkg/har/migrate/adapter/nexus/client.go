package nexus

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/harness/cli/modules/har/pkg/har/migrate/types"
	"github.com/harness/cli/modules/har/pkg/har/migrate/util"

	"github.com/rs/zerolog/log"
)

const (
	connectTimeout = 30 * time.Second

	// A page is built server-side before its first byte is sent, so the limit has
	// to cover the slowest legitimate page, not just network latency.
	defaultListPageTimeout = 5 * time.Minute
	defaultListRetryBase   = 2 * time.Second
	listPageAttempts       = 4
	defaultProgressEvery   = 15 * time.Second
)

type basicTransport struct {
	base     http.RoundTripper
	username string
	password string
}

func (t *basicTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	if t.username != "" {
		req.SetBasicAuth(t.username, t.password)
	}
	req.Header.Set("User-Agent", util.UserAgentString())

	target := req.URL.Redacted()
	log.Debug().Str("method", req.Method).Str("url", target).Msg("Nexus API request")
	started := time.Now()
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		log.Debug().Err(err).Str("method", req.Method).Str("url", target).
			Stringer("elapsed", time.Since(started).Round(time.Millisecond)).Msg("Nexus API request failed")
		return nil, err
	}
	log.Debug().Str("method", req.Method).Str("url", target).Int("status", resp.StatusCode).
		Int64("content_length", resp.ContentLength).Stringer("elapsed_to_headers", time.Since(started).Round(time.Millisecond)).
		Msg("Nexus API response")
	return resp, nil
}

// newClient constructs a nexus client
func newClient(reg *types.RegistryConfig) *client {
	username := reg.Credentials.Username
	password := reg.Credentials.Password
	url := strings.TrimSuffix(reg.Endpoint, "/")

	return &client{
		// No http.Client.Timeout: it would also cut off asset downloads and uploads.
		client: &http.Client{
			Transport: &basicTransport{
				base: &http.Transport{
					Proxy:               http.ProxyFromEnvironment,
					DialContext:         (&net.Dialer{Timeout: connectTimeout}).DialContext,
					TLSHandshakeTimeout: connectTimeout,
					TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
				},
				username: username,
				password: password,
			},
		},
		url:             url,
		insecure:        true,
		username:        username,
		password:        password,
		listPageTimeout: defaultListPageTimeout,
		listRetryBase:   defaultListRetryBase,
		progressEvery:   defaultProgressEvery,
	}
}

type client struct {
	client   *http.Client
	url      string
	insecure bool
	username string
	password string

	listPageTimeout time.Duration
	listRetryBase   time.Duration
	progressEvery   time.Duration
}

// NexusRepository represents a repository from Nexus V3
type NexusRepository struct {
	Name   string `json:"name"`
	Format string `json:"format"`
	Type   string `json:"type"`
	URL    string `json:"url"`
	Online bool   `json:"online"`
}

// NexusAsset represents an asset from Nexus V3
type NexusAsset struct {
	ID         string                 `json:"id"`
	Repository string                 `json:"repository"`
	Format     string                 `json:"format"`
	Path       string                 `json:"path"`
	Checksum   map[string]string      `json:"checksum"`
	FileSize   int64                  `json:"fileSize"`
	Attributes map[string]interface{} `json:"attributes"`
}

// NexusComponent represents a component from Nexus V3
type NexusComponent struct {
	ID         string                 `json:"id"`
	Repository string                 `json:"repository"`
	Format     string                 `json:"format"`
	Group      string                 `json:"group"`
	Name       string                 `json:"name"`
	Version    string                 `json:"version"`
	Assets     []NexusAsset           `json:"assets"`
	Attributes map[string]interface{} `json:"attributes"`
}

// NexusSearchResponse represents the search response from Nexus V3
type NexusSearchResponse struct {
	Items             []NexusComponent `json:"items"`
	ContinuationToken string           `json:"continuationToken"`
}

// NexusRepositoryDetails represents detailed repository information from Nexus V3
type NexusRepositoryDetails struct {
	Name   string             `json:"name"`
	Docker *NexusDockerConfig `json:"docker,omitempty"`
}

// NexusDockerConfig represents Docker-specific configuration
type NexusDockerConfig struct {
	V1Enabled      bool   `json:"v1Enabled"`
	ForceBasicAuth bool   `json:"forceBasicAuth"`
	HttpPort       int    `json:"httpPort,omitempty"`
	HttpsPort      int    `json:"httpsPort,omitempty"`
	Subdomain      string `json:"subdomain,omitempty"`
}

// getRepositories retrieves all repositories from Nexus
func (c *client) getRepositories() ([]NexusRepository, error) {
	url := fmt.Sprintf("%s/service/rest/v1/repositories", strings.TrimSuffix(c.url, "/"))

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var repositories []NexusRepository
	if err := json.NewDecoder(resp.Body).Decode(&repositories); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return repositories, nil
}

// getRepository retrieves a specific repository by name
func (c *client) getRepository(name string) (NexusRepository, error) {
	repositories, err := c.getRepositories()
	if err != nil {
		return NexusRepository{}, err
	}

	for _, repo := range repositories {
		if repo.Name == name {
			return repo, nil
		}
	}

	return NexusRepository{}, fmt.Errorf("repository %s not found", name)
}

// getRepositoryDetails retrieves detailed repository configuration
func (c *client) getRepositoryDetails(name, packageType string) (*NexusRepositoryDetails, error) {
	url := fmt.Sprintf("%s/service/rest/v1/repositories/%s/hosted/%s", strings.TrimSuffix(c.url, "/"), packageType,
		name)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var repoDetails NexusRepositoryDetails
	if err := json.NewDecoder(resp.Body).Decode(&repoDetails); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &repoDetails, nil
}

// streamComponents pages /v1/components and hands each page to fn in order. It uses
// that endpoint because it is database-backed: the search endpoints silently stop at
// 10,000 hits and would truncate large repositories without any error.
func (c *client) streamComponents(
	ctx context.Context,
	repository string,
	progress func(string),
	fn func([]NexusComponent) error,
) error {
	reporter := newPageReporter(repository, progress, c.progressEvery)
	defer reporter.watch()()
	seenTokens := make(map[string]struct{})
	continuationToken := ""
	for page := 1; ; page++ {
		started := time.Now()
		resp, err := c.listComponentsPage(ctx, repository, continuationToken, page, reporter)
		if err != nil {
			return fmt.Errorf("page %d: %w", page, err)
		}
		reporter.pageDone(page, len(resp.Items), time.Since(started))
		if err := fn(resp.Items); err != nil {
			return err
		}
		next := resp.ContinuationToken
		if next == "" {
			return nil
		}
		if _, repeated := seenTokens[next]; repeated {
			return fmt.Errorf("page %d: server returned continuation token %q again; the listing would never finish", page, next)
		}
		seenTokens[next] = struct{}{}
		continuationToken = next
	}
}

// listComponents returns every component in the repository.
func (c *client) listComponents(ctx context.Context, repository string, progress func(string)) ([]NexusComponent, error) {
	var components []NexusComponent
	err := c.streamComponents(ctx, repository, progress, func(items []NexusComponent) error {
		components = append(components, items...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return components, nil
}

// listComponentsPage fetches one page, retrying timeouts, network errors, 429 and
// 5xx with exponential backoff. Other 4xx responses fail immediately.
func (c *client) listComponentsPage(
	ctx context.Context,
	repository, continuationToken string,
	page int,
	reporter *pageReporter,
) (*NexusSearchResponse, error) {
	var err error
	for attempt := 1; ; attempt++ {
		var resp *NexusSearchResponse
		var retryable bool
		reporter.requestStarted(page, attempt)
		resp, retryable, err = c.fetchComponentsPage(ctx, repository, continuationToken)
		if err == nil {
			return resp, nil
		}
		if !retryable || ctx.Err() != nil {
			return nil, err
		}
		if attempt == listPageAttempts {
			return nil, fmt.Errorf("giving up after %d attempts: %w", attempt, err)
		}
		delay := c.listRetryBase << (attempt - 1)
		reporter.retrying(page, attempt, delay, err)
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (c *client) fetchComponentsPage(
	ctx context.Context,
	repository, continuationToken string,
) (page *NexusSearchResponse, retryable bool, err error) {
	reqCtx, cancel := context.WithTimeout(ctx, c.listPageTimeout)
	defer cancel()

	url := fmt.Sprintf("%s/service/rest/v1/components", strings.TrimSuffix(c.url, "/"))
	req, err := http.NewRequestWithContext(reqCtx, "GET", url, nil)
	if err != nil {
		return nil, false, fmt.Errorf("failed to create request: %w", err)
	}

	q := req.URL.Query()
	q.Add("repository", repository)
	if continuationToken != "" {
		q.Add("continuationToken", continuationToken)
	}
	req.URL.RawQuery = q.Encode()

	resp, err := c.client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			err = fmt.Errorf("no response within %s: %w", c.listPageTimeout, err)
		}
		return nil, true, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		retry := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= http.StatusInternalServerError
		return nil, retry, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var decoded NexusSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, true, fmt.Errorf("failed to decode response: %w", err)
	}
	// Unread trailing bytes stop the connection from being reused, and a full
	// listing is thousands of sequential requests.
	_, _ = io.Copy(io.Discard, resp.Body)

	return &decoded, false, nil
}

// pageReporter logs every page and, while a request is in flight, reports its state
// once per interval, so a listing that stalls or takes hours is visibly alive and
// shows what it is waiting on.
type pageReporter struct {
	repository string
	progress   func(string)
	every      time.Duration

	mu           sync.Mutex
	components   int
	page         int
	attempt      int
	requestStart time.Time
	lastPageTook time.Duration
}

func newPageReporter(repository string, progress func(string), every time.Duration) *pageReporter {
	return &pageReporter{repository: repository, progress: progress, every: every}
}

// watch starts the periodic status reports and returns the function that stops them
// and waits for the last one, so nothing prints after the listing returns.
func (p *pageReporter) watch() (stop func()) {
	if p.progress == nil || p.every <= 0 {
		return func() {}
	}
	quit := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(p.every)
		defer ticker.Stop()
		for {
			select {
			case <-quit:
				return
			case <-ticker.C:
				p.report()
			}
		}
	}()
	return func() {
		close(quit)
		<-done
	}
}

func (p *pageReporter) report() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.requestStart.IsZero() {
		return
	}
	p.progress(fmt.Sprintf("Listing %s: fetching page %d (request running %s, attempt %d/%d), "+
		"%d component(s) listed so far, last page took %s",
		p.repository, p.page, time.Since(p.requestStart).Round(time.Second), p.attempt, listPageAttempts,
		p.components, p.lastPageTook.Round(time.Millisecond)))
}

func (p *pageReporter) requestStarted(page, attempt int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.page, p.attempt, p.requestStart = page, attempt, time.Now()
}

func (p *pageReporter) pageDone(page, items int, took time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.components += items
	p.lastPageTook = took
	p.requestStart = time.Time{}
	log.Info().Str("repository", p.repository).Int("page", page).Int("components", p.components).
		Stringer("page_duration", took.Round(time.Millisecond)).Msg("Listed Nexus components page")
}

func (p *pageReporter) retrying(page, attempt int, delay time.Duration, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requestStart = time.Time{}
	log.Warn().Err(err).Str("repository", p.repository).Int("page", page).Int("attempt", attempt).
		Stringer("retry_in", delay).Msg("Nexus component listing request failed, retrying")
	if p.progress != nil {
		p.progress(fmt.Sprintf("Listing %s: page %d failed (%v); retry %d/%d in %s",
			p.repository, page, err, attempt, listPageAttempts-1, delay))
	}
}

// getAsset downloads an asset by its download URL
func (c *client) getAsset(downloadURL string) (io.ReadCloser, http.Header, error) {
	req, err := http.NewRequest("GET", downloadURL, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to execute request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return resp.Body, resp.Header, nil
}

// uploadAsset uploads an asset to Nexus
func (c *client) uploadAsset(repository string, file io.ReadCloser, filename string, path string) error {
	url := fmt.Sprintf("%s/service/rest/v1/components", strings.TrimSuffix(c.url, "/"))

	// Create multipart form data using standard library
	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)

	// Process multipart form asynchronously
	go func() {
		defer pw.Close()
		defer writer.Close()

		// Add repository field
		if err := writer.WriteField("repository", repository); err != nil {
			pw.CloseWithError(err)
			return
		}

		// Add the file
		part, err := writer.CreateFormFile("asset", filename)
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

	req, err := http.NewRequest("POST", url, pr)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return nil
}

// getFileName extracts filename from a path
func getFileName(path string) string {
	parts := strings.Split(path, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return path
}

// buildDownloadURL constructs the download URL for an asset
func (c *client) buildDownloadURL(repository, path string) string {
	return fmt.Sprintf("%s/repository/%s/%s", strings.TrimSuffix(c.url, "/"), repository, strings.TrimPrefix(path, "/"))
}

// getDockerPort attempts to discover the Docker HTTPS port for a repository
func (c *client) getDockerPort(repositoryName string, insecure bool) (int, error) {
	repoDetails, err := c.getRepositoryDetails(repositoryName, "docker")
	if err != nil {
		return 0, fmt.Errorf("failed to get repository details: %w", err)
	}

	// Check for Docker configuration
	if repoDetails.Docker == nil {
		return 0, fmt.Errorf("no Docker configuration found for repository %s", repositoryName)
	}

	if insecure {
		if repoDetails.Docker.HttpPort > 0 {
			return repoDetails.Docker.HttpPort, nil
		}
	}

	// Prefer HTTPS port, fallback to HTTP port
	if repoDetails.Docker.HttpsPort > 0 {
		return repoDetails.Docker.HttpsPort, nil
	}

	if repoDetails.Docker.HttpPort > 0 {
		return repoDetails.Docker.HttpPort, nil
	}

	return 0, fmt.Errorf("no Docker connector port configured for repository %s", repositoryName)
}
