// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package nexus

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"
	"golang.org/x/net/html"

	adp "github.com/harness/cli/modules/har/pkg/har/migrate/adapter"
	"github.com/harness/cli/modules/har/pkg/har/migrate/types"
)

const (
	// Past this many hits Nexus search answers an empty page without a continuation
	// token, which looks exactly like the real end of the results.
	searchResultCap = 10000

	mavenMetadataFile = "maven-metadata.xml"

	// Printed by the browse page when a folder has more entries than
	// nexus.browse.component.tree.maxHtmlNodes; the rest are left out.
	browseTruncatedMarker = "There may be additional items to display"

	maxReportedMissing = 20
)

var errNotFound = errors.New("not found")

// mavenArtifact is an artifact, or one version of it when version is set.
type mavenArtifact struct{ group, name, version string }

func (a mavenArtifact) String() string {
	if a.version != "" {
		return a.group + ":" + a.name + ":" + a.version
	}
	return a.group + ":" + a.name
}

func (a mavenArtifact) path() string {
	p := strings.ReplaceAll(a.group, ".", "/") + "/" + a.name
	if a.version != "" {
		p += "/" + a.version
	}
	return p
}

// A unique snapshot build such as 1.0-20240101.120000-3 belongs to the 1.0-SNAPSHOT folder.
var snapshotBuild = regexp.MustCompile(`-\d{8}\.\d{6}-\d+$`)

func inVersionFolder(version, folder string) bool {
	return version == folder ||
		(strings.HasSuffix(folder, "-SNAPSHOT") && snapshotBuild.ReplaceAllString(version, "-SNAPSHOT") == folder)
}

// artifactAt maps a repository folder (group path segments + artifactId) to its artifact.
func artifactAt(dir []string) mavenArtifact {
	return mavenArtifact{group: strings.Join(dir[:len(dir)-1], "."), name: dir[len(dir)-1]}
}

type browseEntry struct {
	name   string
	folder bool
}

type mavenMetadata struct {
	ArtifactID string `xml:"artifactId"`
	Versioning struct {
		Snapshot *struct{} `xml:"snapshot"`
		Versions []string  `xml:"versions>version"`
	} `xml:"versioning"`
}

// parallelLister lists a Maven repository without the sequential /v1/components
// chain: it walks the browse tree to find artifacts and lists each artifact's
// components through search. Search is capped per query, so every query is scoped to
// one artifact, and an artifact that reaches the cap is re-listed per version.
type parallelLister struct {
	c          *client
	repository string
	sem        chan struct{}
	cancel     context.CancelCauseFunc

	emitMu sync.Mutex
	fn     func([]NexusComponent) error
	seen   map[string]struct{}

	stateMu   sync.Mutex
	artifacts map[mavenArtifact]struct{}
	missing   []mavenArtifact

	folders    atomic.Int64
	searched   atomic.Int64
	components atomic.Int64
}

// streamMavenParallel hands every component of a Maven repository to fn using up to
// opts.Concurrency requests at a time. fn is never called concurrently.
func (c *client) streamMavenParallel(
	ctx context.Context,
	repository string,
	opts adp.ListOptions,
	fn func([]NexusComponent) error,
) error {
	concurrency, progress := opts.Concurrency, opts.Progress
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	l := &parallelLister{
		c:          c,
		repository: repository,
		sem:        make(chan struct{}, concurrency),
		cancel:     cancel,
		fn:         fn,
		seen:       make(map[string]struct{}),
		artifacts:  make(map[mavenArtifact]struct{}),
	}
	started := time.Now()
	stopReports := l.watch(progress, concurrency, started)

	found := make(chan mavenArtifact, 4*concurrency)
	var crawling sync.WaitGroup
	crawling.Add(1)
	go l.crawl(ctx, &crawling, nil, found)
	go func() {
		crawling.Wait()
		close(found)
	}()

	var searching sync.WaitGroup
	for range concurrency {
		searching.Add(1)
		go func() {
			defer searching.Done()
			// Keep draining after a failure so blocked crawlers can see the cancellation.
			for a := range found {
				if ctx.Err() != nil {
					continue
				}
				if err := l.listArtifact(ctx, a); err != nil {
					cancel(err)
				}
			}
		}()
	}
	searching.Wait()
	stopReports()

	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	return l.finish(opts, started)
}

func (l *parallelLister) finish(opts adp.ListOptions, started time.Time) error {
	l.stateMu.Lock()
	defer l.stateMu.Unlock()
	if len(l.artifacts) == 0 {
		return fmt.Errorf("the browse tree of repository %s lists no Maven artifacts; if the repository is not empty, "+
			"run the Nexus task \"Repair - Rebuild repository browse\" or remove listConcurrency to use the sequential listing",
			l.repository)
	}
	took := time.Since(started).Round(time.Second)
	log.Info().Str("repository", l.repository).Int("artifacts", len(l.artifacts)).
		Int64("components", l.components.Load()).Int64("folders", l.folders.Load()).
		Stringer("duration", took).Msg("Listed Nexus repository in parallel")
	if opts.Progress != nil {
		opts.Progress(fmt.Sprintf("Listed %s: %d component(s) from %d artifact(s) in %s",
			l.repository, l.components.Load(), len(l.artifacts), took))
	}
	if len(l.missing) == 0 {
		return nil
	}
	slices.SortFunc(l.missing, func(a, b mavenArtifact) int { return strings.Compare(a.String(), b.String()) })
	shown := l.missing[:min(len(l.missing), maxReportedMissing)]
	names := make([]string, len(shown))
	for i, a := range shown {
		names[i] = a.String()
	}
	log.Warn().Str("repository", l.repository).Int("artifacts", len(l.missing)).Strs("examples", names).
		Msg("Search returned no components for artifacts found in the browse tree")
	if opts.Warn != nil {
		opts.Warn(fmt.Sprintf("%s: search returned no components for %d artifact folder(s) found in the browse tree "+
			"(e.g. %s); they were not migrated and are recorded as skipped (%s). They are either empty or missing "+
			"from the Nexus search index; if they should have content, run \"Repair - Rebuild repository search\" "+
			"in Nexus or migrate them without listConcurrency",
			l.repository, len(l.missing), strings.Join(names, ", "), types.SkipReasonNotInSearchIndex))
	}
	if opts.Unlisted != nil {
		for _, a := range l.missing {
			opts.Unlisted(a.String(), a.path())
		}
	}
	return nil
}

func (l *parallelLister) crawl(ctx context.Context, wg *sync.WaitGroup, dir []string, found chan<- mavenArtifact) {
	defer wg.Done()
	if ctx.Err() != nil {
		return
	}
	entries, err := l.browse(ctx, dir)
	if errors.Is(err, errNotFound) && len(dir) > 0 {
		// Deleted between listing its parent and visiting it.
		return
	}
	if err != nil {
		l.cancel(fmt.Errorf("browse %s/%s: %w", l.repository, strings.Join(dir, "/"), err))
		return
	}
	l.folders.Add(1)

	versions, artifact, err := l.classify(ctx, dir, entries)
	if err != nil {
		l.cancel(fmt.Errorf("read %s/%s: %w", l.repository, strings.Join(dir, "/"), err))
		return
	}
	if artifact != nil && l.addArtifact(*artifact) {
		select {
		case found <- *artifact:
		case <-ctx.Done():
			return
		}
	}
	for _, e := range entries {
		if !e.folder {
			continue
		}
		// Version folders are covered by the artifact's search. Any other subfolder may
		// hold a nested group (com/acme/lib and com/acme/lib/plugin/...), or a version
		// the metadata does not list, which resolves to the same artifact again.
		if _, isVersion := versions[e.name]; isVersion {
			continue
		}
		wg.Add(1)
		go l.crawl(ctx, wg, append(slices.Clone(dir), e.name), found)
	}
}

// classify reports whether dir is an artifact folder (or a version folder of one) and
// which of its subfolders are that artifact's versions.
func (l *parallelLister) classify(
	ctx context.Context,
	dir []string,
	entries []browseEntry,
) (versions map[string]struct{}, artifact *mavenArtifact, err error) {
	hasMetadata, hasContent := false, false
	for _, e := range entries {
		switch {
		case e.folder:
		case e.name == mavenMetadataFile:
			hasMetadata = true
		case !strings.HasPrefix(e.name, mavenMetadataFile+"."):
			hasContent = true
		}
	}

	// Group folders of Maven plugins also carry a maven-metadata.xml, but without an
	// artifactId, so the file has to be read to tell them apart.
	if hasMetadata && len(dir) >= 2 {
		md, err := l.metadata(ctx, dir)
		switch {
		case errors.Is(err, errNotFound):
		case err != nil:
			return nil, nil, err
		case md.ArtifactID != "" && md.Versioning.Snapshot != nil && len(dir) >= 3:
			a := artifactAt(dir[:len(dir)-1])
			return nil, &a, nil
		case md.ArtifactID != "":
			versions = make(map[string]struct{}, len(md.Versioning.Versions))
			for _, v := range md.Versioning.Versions {
				versions[v] = struct{}{}
			}
			a := artifactAt(dir)
			return versions, &a, nil
		}
	}
	if hasContent && len(dir) >= 3 {
		a := artifactAt(dir[:len(dir)-1])
		return nil, &a, nil
	}
	return nil, nil, nil
}

func (l *parallelLister) addArtifact(a mavenArtifact) bool {
	l.stateMu.Lock()
	defer l.stateMu.Unlock()
	if _, ok := l.artifacts[a]; ok {
		return false
	}
	l.artifacts[a] = struct{}{}
	return true
}

func (l *parallelLister) listArtifact(ctx context.Context, a mavenArtifact) error {
	raw, matched, err := l.search(ctx, a, "")
	if err != nil {
		return fmt.Errorf("search %s: %w", a, err)
	}
	if raw >= searchResultCap {
		log.Warn().Str("repository", l.repository).Str("artifact", a.String()).Int("hits", raw).
			Msg("Search hit its result cap; listing the artifact per version")
		if matched, err = l.searchPerVersion(ctx, a); err != nil {
			return err
		}
	}
	l.searched.Add(1)
	if matched == 0 && raw < searchResultCap {
		l.stateMu.Lock()
		l.missing = append(l.missing, a)
		l.stateMu.Unlock()
	}
	return nil
}

func (l *parallelLister) searchPerVersion(ctx context.Context, a mavenArtifact) (int, error) {
	dir := append(strings.Split(a.group, "."), a.name)
	entries, err := l.browse(ctx, dir)
	if err != nil {
		return 0, fmt.Errorf("browse versions of %s: %w", a, err)
	}
	total := 0
	for _, e := range entries {
		if !e.folder {
			continue
		}
		raw, matched, err := l.search(ctx, a, e.name)
		if err != nil {
			return 0, fmt.Errorf("search %s version %s: %w", a, e.name, err)
		}
		if raw >= searchResultCap {
			return 0, fmt.Errorf("search for %s version %s reached the %d result cap of Nexus search; "+
				"remove listConcurrency to use the sequential listing", a, e.name, searchResultCap)
		}
		if matched == 0 {
			l.stateMu.Lock()
			l.missing = append(l.missing, mavenArtifact{group: a.group, name: a.name, version: e.name})
			l.stateMu.Unlock()
		}
		total += matched
	}
	return total, nil
}

// search pages one artifact's components (optionally one version folder) and emits those that
// match exactly. raw counts every hit so a capped result can be recognised.
func (l *parallelLister) search(ctx context.Context, a mavenArtifact, version string) (raw, matched int, err error) {
	token := ""
	for {
		q := url.Values{}
		q.Set("repository", l.repository)
		// The maven.* attributes are indexed verbatim; group and name are analyzed and
		// would also match other artifacts.
		q.Set("maven.groupId", a.group)
		q.Set("maven.artifactId", a.name)
		switch {
		case strings.HasSuffix(version, "-SNAPSHOT"):
			// Snapshot builds are stored under their timestamped version.
			q.Set("maven.baseVersion", version)
		case version != "":
			q.Set("version", version)
		}
		if token != "" {
			q.Set("continuationToken", token)
		}
		var page NexusSearchResponse
		err := l.get(ctx, l.c.url+"/service/rest/v1/search?"+q.Encode(), func(r io.Reader) error {
			return json.NewDecoder(r).Decode(&page)
		})
		if err != nil {
			return raw, matched, err
		}
		raw += len(page.Items)
		var hits []NexusComponent
		for _, item := range page.Items {
			if item.Group == a.group && item.Name == a.name && (version == "" || inVersionFolder(item.Version, version)) {
				hits = append(hits, item)
			}
		}
		matched += len(hits)
		if err := l.emit(ctx, hits); err != nil {
			return raw, matched, err
		}
		next := page.ContinuationToken
		if next == "" {
			return raw, matched, nil
		}
		if next == token {
			return raw, matched, fmt.Errorf("server returned continuation token %q again", next)
		}
		token = next
	}
}

func (l *parallelLister) emit(ctx context.Context, items []NexusComponent) error {
	if len(items) == 0 {
		return nil
	}
	l.emitMu.Lock()
	defer l.emitMu.Unlock()
	// A worker can finish a page after another one failed; fn must not see it.
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	fresh := make([]NexusComponent, 0, len(items))
	for _, item := range items {
		key := item.ID
		if key == "" {
			key = item.Group + ":" + item.Name + ":" + item.Version
		}
		if _, dup := l.seen[key]; dup {
			continue
		}
		l.seen[key] = struct{}{}
		fresh = append(fresh, item)
	}
	if len(fresh) == 0 {
		return nil
	}
	l.components.Add(int64(len(fresh)))
	return l.fn(fresh)
}

func (l *parallelLister) browse(ctx context.Context, dir []string) ([]browseEntry, error) {
	var entries []browseEntry
	var truncated bool
	err := l.get(ctx, l.c.url+"/service/rest/repository/browse/"+l.repoPath(dir, true), func(r io.Reader) error {
		var err error
		entries, truncated, err = parseBrowsePage(r)
		return err
	})
	if err != nil {
		return nil, err
	}
	if truncated {
		return nil, fmt.Errorf("the browse page lists only part of this folder (Nexus limit " +
			"nexus.browse.component.tree.maxHtmlNodes); remove listConcurrency to use the sequential listing")
	}
	return entries, nil
}

func (l *parallelLister) metadata(ctx context.Context, dir []string) (*mavenMetadata, error) {
	var md mavenMetadata
	err := l.get(ctx, l.c.url+"/repository/"+l.repoPath(append(slices.Clone(dir), mavenMetadataFile), false),
		func(r io.Reader) error { return xml.NewDecoder(r).Decode(&md) })
	if err != nil {
		return nil, err
	}
	return &md, nil
}

func (l *parallelLister) repoPath(segments []string, folder bool) string {
	parts := make([]string, 0, len(segments)+1)
	parts = append(parts, url.PathEscape(l.repository))
	for _, s := range segments {
		parts = append(parts, url.PathEscape(s))
	}
	p := strings.Join(parts, "/")
	if folder {
		p += "/"
	}
	return p
}

// get runs one listing request within the shared concurrency limit.
func (l *parallelLister) get(ctx context.Context, rawURL string, read func(io.Reader) error) error {
	select {
	case l.sem <- struct{}{}:
	case <-ctx.Done():
		return context.Cause(ctx)
	}
	defer func() { <-l.sem }()
	return l.c.getWithRetry(ctx, rawURL, read)
}

func (l *parallelLister) watch(progress func(string), concurrency int, started time.Time) (stop func()) {
	if progress == nil || l.c.progressEvery <= 0 {
		return func() {}
	}
	quit := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(l.c.progressEvery)
		defer ticker.Stop()
		for {
			select {
			case <-quit:
				return
			case <-ticker.C:
				l.stateMu.Lock()
				artifacts := len(l.artifacts)
				l.stateMu.Unlock()
				progress(fmt.Sprintf("Listing %s (%d parallel requests): %d folder(s) browsed, %d artifact(s) found, "+
					"%d searched, %d component(s) listed, %s elapsed",
					l.repository, concurrency, l.folders.Load(), artifacts, l.searched.Load(),
					l.components.Load(), time.Since(started).Round(time.Second)))
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(quit)
			<-done
		})
	}
}

// getWithRetry GETs rawURL and passes the body to read, retrying timeouts, network
// errors, 429 and 5xx with exponential backoff. 404 returns errNotFound.
func (c *client) getWithRetry(ctx context.Context, rawURL string, read func(io.Reader) error) error {
	for attempt := 1; ; attempt++ {
		retryable, err := c.getOnce(ctx, rawURL, read)
		if err == nil || !retryable || ctx.Err() != nil {
			return err
		}
		if attempt == listPageAttempts {
			return fmt.Errorf("giving up after %d attempts: %w", attempt, err)
		}
		delay := c.listRetryBase << (attempt - 1)
		log.Warn().Err(err).Str("url", rawURL).Int("attempt", attempt).Stringer("retry_in", delay).
			Msg("Nexus listing request failed, retrying")
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
}

func (c *client) getOnce(ctx context.Context, rawURL string, read func(io.Reader) error) (retryable bool, err error) {
	reqCtx, cancel := context.WithTimeout(ctx, c.listPageTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return false, fmt.Errorf("failed to create request: %w", err)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			err = fmt.Errorf("no response within %s: %w", c.listPageTimeout, err)
		}
		return true, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return false, errNotFound
	case resp.StatusCode != http.StatusOK:
		retry := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= http.StatusInternalServerError
		return retry, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}
	if err := read(resp.Body); err != nil {
		return true, fmt.Errorf("failed to read response: %w", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return false, nil
}

// parseBrowsePage reads the entries of a Nexus HTML browse page. Folder links are
// relative and end in "/"; file links are absolute asset URLs.
func parseBrowsePage(r io.Reader) (entries []browseEntry, truncated bool, err error) {
	z := html.NewTokenizer(r)
	var inLink bool
	var href string
	var text strings.Builder
	for {
		switch z.Next() {
		case html.ErrorToken:
			if errors.Is(z.Err(), io.EOF) {
				return entries, truncated, nil
			}
			return nil, false, z.Err()
		case html.StartTagToken:
			name, hasAttr := z.TagName()
			if string(name) != "a" {
				continue
			}
			inLink, href = true, ""
			text.Reset()
			for hasAttr {
				var key, val []byte
				key, val, hasAttr = z.TagAttr()
				if string(key) == "href" {
					href = string(val)
				}
			}
		case html.TextToken:
			if inLink {
				text.Write(z.Text())
			} else if bytes.Contains(z.Text(), []byte(browseTruncatedMarker)) {
				truncated = true
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			if string(name) != "a" || !inLink {
				continue
			}
			inLink = false
			entryName := strings.TrimSpace(text.String())
			if href == "../" || entryName == "" {
				continue
			}
			entries = append(entries, browseEntry{name: entryName, folder: strings.HasSuffix(href, "/")})
		}
	}
}
