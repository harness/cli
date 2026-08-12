// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package migratable

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/harness/cli/modules/har/pkg/har/migrate/types"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/rs/zerolog"
)

// noopIndexAdapter provides zero-value implementations of every adapter.Adapter
// method so tests only need to override what they exercise.
type noopIndexAdapter struct{}

func (noopIndexAdapter) GetKeyChain(string) (authn.Keychain, error) { return nil, nil }
func (noopIndexAdapter) GetConfig() types.RegistryConfig            { return types.RegistryConfig{} }
func (noopIndexAdapter) ValidateCredentials() (bool, error)         { return false, nil }
func (noopIndexAdapter) GetRegistry(context.Context, string) (types.RegistryInfo, error) {
	return types.RegistryInfo{}, nil
}
func (noopIndexAdapter) CreateRegistryIfDoesntExist(string) (bool, error) { return false, nil }
func (noopIndexAdapter) GetPackages(string, types.ArtifactType, *types.TreeNode) ([]types.Package, error) {
	return nil, nil
}
func (noopIndexAdapter) GetVersions(types.Package, *types.TreeNode, string, string, types.ArtifactType) ([]types.Version, error) {
	return nil, nil
}
func (noopIndexAdapter) GetFiles(string) ([]types.File, error) { return nil, nil }
func (noopIndexAdapter) DownloadFile(string, string) (io.ReadCloser, http.Header, error) {
	return nil, nil, nil
}
func (noopIndexAdapter) UploadFile(string, io.ReadCloser, *types.File, http.Header, string, string, types.ArtifactType, map[string]interface{}) error {
	return nil
}
func (noopIndexAdapter) GetOCIImagePath(string, string, string) (string, error) { return "", nil }
func (noopIndexAdapter) AddNPMTag(string, string, string, string) error         { return nil }
func (noopIndexAdapter) VersionExists(context.Context, types.Package, string, string, string, types.ArtifactType) (bool, error) {
	return false, nil
}
func (noopIndexAdapter) FileExists(context.Context, string, string, string, *types.File, types.ArtifactType) (bool, error) {
	return false, nil
}
func (noopIndexAdapter) GetAllFilesForVersion(context.Context, string, string, string) ([]string, error) {
	return nil, nil
}
func (noopIndexAdapter) CreateVersion(string, string, string, types.ArtifactType, []*types.PackageFiles, map[string]interface{}) error {
	return nil
}
func (noopIndexAdapter) SearchFiles(string) ([]types.SearchedFile, error) { return nil, nil }
func (noopIndexAdapter) BuildExistingIndex(context.Context, string, int) (*types.ExistingIndex, error) {
	return nil, nil
}

// indexFakeSrc serves file bytes keyed by URI for the Version.Migrate file loop.
type indexFakeSrc struct {
	noopIndexAdapter
	content map[string][]byte
}

func (s *indexFakeSrc) DownloadFile(_ string, uri string) (io.ReadCloser, http.Header, error) {
	b, ok := s.content[uri]
	if !ok {
		return nil, nil, io.ErrUnexpectedEOF
	}
	return io.NopCloser(strings.NewReader(string(b))), http.Header{}, nil
}

// indexFakeDest records the f.Name of every file it is asked to upload.
type indexFakeDest struct {
	noopIndexAdapter
	uploaded []string
}

func (d *indexFakeDest) UploadFile(
	_ string,
	file io.ReadCloser,
	f *types.File,
	_ http.Header,
	_ string,
	_ string,
	_ types.ArtifactType,
	_ map[string]interface{},
) error {
	if file != nil {
		_, _ = io.Copy(io.Discard, file)
		_ = file.Close()
	}
	d.uploaded = append(d.uploaded, f.Name)
	return nil
}

// nugetFileTree builds a tree of leaf file nodes so Version.Migrate's
// tree.GetAllFiles walk yields exactly those files, each keyed by its full Uri.
func nugetFileTree(uris ...string) *types.TreeNode {
	root := &types.TreeNode{Name: "root", Key: "/", IsLeaf: false}
	for _, uri := range uris {
		name := uri[strings.LastIndex(uri, "/")+1:]
		f := &types.File{Name: name, Uri: uri, Size: len(uri)}
		root.Children = append(root.Children, types.TreeNode{
			Name:   name,
			Key:    uri,
			IsLeaf: true,
			File:   f,
		})
	}
	return root
}

func newVersionJobForIndexTest(src *indexFakeSrc, dest *indexFakeDest, node *types.TreeNode, stats *types.TransferStats, idx *types.ExistingIndex) *Version {
	return &Version{
		srcRegistry:   "src-reg",
		destRegistry:  "dst-reg",
		srcAdapter:    src,
		destAdapter:   dest,
		artifactType:  types.NUGET,
		logger:        zerolog.Nop(),
		pkg:           types.Package{Name: "hello.foo.bar.xxx.yyy"},
		version:       types.Version{Name: "3.203.0-pr-280.a52f7f9.1"},
		node:          node,
		stats:         stats,
		config:        &types.Config{Concurrency: 1, DryRun: false, Overwrite: false},
		existingIndex: idx,
	}
}

// TestVersionMigrateSkipsFileByUriNotBasename is a regression guard: HasFile
// must be queried with the file's full source-relative Uri, not its basename.
// Two files sharing a basename but living under different nested JFrog
// folders must be distinguished — a basename-only query would either miss
// (re-upload an already-migrated file) or collide (skip a file that was
// never migrated). NuGet's ambiguous package layout makes this the type most
// likely to hit nested folders with repeated basenames.
func TestVersionMigrateSkipsFileByUriNotBasename(t *testing.T) {
	idx := types.NewExistingIndex()
	// Only the nested-folder copy is recorded as already migrated.
	idx.AddFile("hello.foo.bar.xxx.yyy", "3.203.0-pr-280.a52f7f9.1",
		"/hello/hello.foo.bar.xxx.yyy/3.203.0-INTEGRATION/hello.foo.bar.xxx.yyy.3.203.0-pr-280.a52f7f9.1.nupkg")

	const rootURI = "/hello.foo.bar.xxx.yyy.3.203.0-pr-280.a52f7f9.1.nupkg"
	const nestedURI = "/hello/hello.foo.bar.xxx.yyy/3.203.0-INTEGRATION/hello.foo.bar.xxx.yyy.3.203.0-pr-280.a52f7f9.1.nupkg"

	src := &indexFakeSrc{content: map[string][]byte{
		rootURI:   []byte("root-copy"),
		nestedURI: []byte("nested-copy"),
	}}
	dest := &indexFakeDest{}
	stats := &types.TransferStats{}

	node := nugetFileTree(rootURI, nestedURI)
	job := newVersionJobForIndexTest(src, dest, node, stats, idx)

	if err := job.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate() failed: %v", err)
	}

	// Only the root-level file (not in the index) should have been uploaded;
	// the nested one is already recorded and must be skipped.
	if len(dest.uploaded) != 1 || dest.uploaded[0] != "hello.foo.bar.xxx.yyy.3.203.0-pr-280.a52f7f9.1.nupkg" {
		t.Errorf("dest uploads = %v, want exactly the root-level file", dest.uploaded)
	}

	var skipped, uploaded int
	for _, s := range stats.FileStats {
		switch s.Status {
		case types.StatusSkip:
			skipped++
			if s.Uri != nestedURI {
				t.Errorf("skip stat for unexpected file %q, want %q", s.Uri, nestedURI)
			}
		case types.StatusSuccess:
			uploaded++
			if s.Uri != rootURI {
				t.Errorf("upload stat for unexpected file %q, want %q", s.Uri, rootURI)
			}
		}
	}
	if skipped != 1 {
		t.Errorf("skipped = %d, want 1", skipped)
	}
	if uploaded != 1 {
		t.Errorf("uploaded = %d, want 1", uploaded)
	}
}
