package nexus

import (
	"context"
	"fmt"
	"sync"

	adp "github.com/harness/cli/modules/har/pkg/har/migrate/adapter"
	"github.com/harness/cli/modules/har/pkg/har/migrate/types"
)

// listing is one complete, immutable snapshot of a repository's components. It is
// built once so GetFiles, GetPackages and GetVersions share a single pass instead of
// each re-listing the repository (once per package for GetVersions).
type listing struct {
	components []NexusComponent
	files      []types.File
	// byGroupName indexes components by "group/name", the Maven package name.
	byGroupName map[string][]int
}

func newListing(repository string, components []NexusComponent) *listing {
	l := &listing{
		components:  components,
		byGroupName: make(map[string][]int),
	}
	for i, component := range components {
		key := component.Group + "/" + component.Name
		l.byGroupName[key] = append(l.byGroupName[key], i)
		for _, asset := range component.Assets {
			l.files = append(l.files, types.File{
				Name:     getFileName(asset.Path),
				Registry: repository,
				Uri:      asset.Path,
				Size:     int(asset.FileSize),
				SHA1:     asset.Checksum["sha1"],
				SHA2:     asset.Checksum["sha256"],
			})
		}
	}
	return l
}

type listingEntry struct {
	mu sync.Mutex
	l  *listing
}

// listingCache is a per-repository single-flight cache: concurrent callers for the
// same repository wait for one listing, while other repositories list independently.
type listingCache struct {
	mu      sync.Mutex
	entries map[string]*listingEntry
}

func (c *listingCache) get(repository string, load func() ([]NexusComponent, error)) (*listing, error) {
	c.mu.Lock()
	if c.entries == nil {
		c.entries = make(map[string]*listingEntry)
	}
	e, ok := c.entries[repository]
	if !ok {
		e = &listingEntry{}
		c.entries[repository] = e
	}
	c.mu.Unlock()

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.l != nil {
		return e.l, nil
	}
	components, err := load()
	if err != nil {
		return nil, err
	}
	e.l = newListing(repository, components)
	return e.l, nil
}

func (c *listingCache) invalidate(repository string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, repository)
}

func (a *adapter) listing(registry string, opts adp.ListOptions) (*listing, error) {
	l, err := a.listings.get(registry, func() ([]NexusComponent, error) {
		return a.client.listComponents(context.Background(), registry, opts)
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list components in registry %s: %w", registry, err)
	}
	return l, nil
}

// scopedTo returns an adapter that serves l as the complete contents of registry, so
// the migration of one batch never triggers or sees the rest of the repository.
func (a *adapter) scopedTo(registry string, l *listing) *adapter {
	scoped := &adapter{client: a.client, reg: a.reg}
	scoped.listings.entries = map[string]*listingEntry{registry: {l: l}}
	return scoped
}
