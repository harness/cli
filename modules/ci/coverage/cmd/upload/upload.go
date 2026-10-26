// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package upload

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"golang.org/x/sync/errgroup"

	covclient "github.com/harness/cli/v3/modules/ci/coverage/client"
	"github.com/harness/cli/v3/modules/ci/coverage/env"
	covfile "github.com/harness/cli/v3/modules/ci/coverage/file"
	"github.com/harness/cli/v3/modules/ci/coverage/format"
	"github.com/harness/cli/v3/pkg/client"
	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/hlog"
)

var errCoverageExecutionIDMandatory = errors.New("execution ID is mandatory")

// uploadFlags holds the raw --flag values for `execute coverage:upload`.
type uploadFlags struct {
	accountID       string
	provider        string
	owner           string
	identifier      string
	commitID        string
	branch          string
	buildID         int
	pipelineID      string
	executionID     string
	pullReqID       int64
	targetScope     string
	tags            []string
	files           []string
	base            string
	patch           bool
	excludePatterns []string
}

// uploadParams is the fully resolved set of parameters that drive
// report upload, after CI-vendor defaulting (resolveUploadParams).
type uploadParams struct {
	accountID     string
	provider      string
	baseURL       string
	owner         string
	identifier    string
	commitID      string
	defaultBranch string
	branch        string
	buildID       int
	pipelineID    string
	executionID   string
	pullReqID     int64
	resolvedScope covclient.Scope
	tags          []string
	base          string
	commitBefore  string
	patch         bool
}

// resolveUploadParams fills in any unset flag with CI-vendor-detected
// values, mirroring HCli's upload checkFlags. A failed vendor lookup is not
// fatal: the zero-value env.Vendor still resolves owner/identifier/commit
// from the local git remote and HEAD.
func resolveUploadParams(f uploadFlags) (*uploadParams, error) {
	vendor, err := env.Info()
	if err != nil {
		hlog.Debug("no CI vendor detected, will use git remote for SCM detection", "err", err)
	}

	accountID := f.accountID
	if accountID == "" {
		accountID, err = vendor.AccountID()
		if err != nil {
			return nil, fmt.Errorf("failed to get account ID: %w", err)
		}
	}

	provider := f.provider
	if provider == "" {
		provider, err = vendor.SCM()
		if err != nil {
			return nil, fmt.Errorf("failed to get SCM provider: %w", err)
		}
	}

	baseURL, err := vendor.RepoBaseURL()
	if err != nil {
		return nil, fmt.Errorf("failed to get repo base URL: %w", err)
	}
	if baseURL == "" {
		return nil, fmt.Errorf("failed to get base URL")
	}

	owner := f.owner
	if owner == "" {
		owner, err = vendor.Owner()
		if err != nil {
			return nil, fmt.Errorf("failed to get SCM owner: %w", err)
		}
	}

	identifier := f.identifier
	if identifier == "" {
		identifier, err = vendor.Identifier()
		if err != nil {
			return nil, fmt.Errorf("failed to get SCM identifier: %w", err)
		}
		if strings.EqualFold(provider, env.HarnessProvider) {
			identifier, owner = parseIdentifierOwner(accountID, owner, identifier)
		}
	}

	commitID := f.commitID
	if commitID == "" {
		commitID, err = vendor.Commit()
		if err != nil {
			return nil, fmt.Errorf("failed to get SCM commit ID: %w", err)
		}
	}

	// DefaultBranch is optional; ignore the error if unavailable.
	defaultBranch, _ := vendor.DefaultBranch()

	var commitBefore string
	if vendor.IsValidCommitBefore() {
		commitBefore = vendor.CommitBefore()
		if commitBefore == commitID {
			hlog.Debug("commit_before equals commit_id, no diff available", "commit", commitBefore)
			commitBefore = ""
		}
	}

	branch := f.branch
	if branch == "" {
		branch, err = vendor.Branch()
		if err != nil {
			return nil, fmt.Errorf("failed to get SCM branch: %w", err)
		}
	}

	base := f.base
	if base == "" {
		base, err = vendor.Base()
		if err != nil {
			return nil, fmt.Errorf("failed to get SCM base branch: %w", err)
		}
	}

	// For non-PR builds (e.g. push to a feature branch), fall back to the
	// default branch so diff coverage still works without an explicit --base.
	if base == "" && defaultBranch != "" && branch != "" && branch != defaultBranch {
		base = defaultBranch
	}

	buildID := f.buildID
	if buildID == 0 {
		buildID = vendor.Build()
	}

	pipelineID := f.pipelineID
	if pipelineID == "" {
		pipelineID = path.Join(accountID, vendor.OrgID(), vendor.ProjectID(), vendor.Pipeline())
	}

	resolvedScope, err := covclient.ResolveScope(accountID, provider, owner, f.targetScope, pipelineID)
	if err != nil {
		return nil, err
	}

	executionID := f.executionID
	if executionID == "" {
		executionID = vendor.Execution()
		if executionID == "" {
			return nil, errCoverageExecutionIDMandatory
		}
	}

	pullReqID := f.pullReqID
	if pullReqID == 0 {
		pullReqID = vendor.PullRequestID()
	}

	if f.patch && base == "" {
		return nil, fmt.Errorf("--patch requires --base to be specified")
	}

	return &uploadParams{
		accountID:     accountID,
		provider:      provider,
		baseURL:       baseURL,
		owner:         owner,
		identifier:    identifier,
		commitID:      commitID,
		defaultBranch: defaultBranch,
		branch:        branch,
		buildID:       buildID,
		pipelineID:    pipelineID,
		executionID:   executionID,
		pullReqID:     pullReqID,
		resolvedScope: resolvedScope,
		tags:          f.tags,
		base:          base,
		commitBefore:  commitBefore,
		patch:         f.patch,
	}, nil
}

// parseIdentifierOwner reparses owner/identifier for the Harness
// provider: Identifier() returns a Harness Code path such as
// "<account>/<org>/<project>/<repo>", not a bare repo identifier. If
// identifier contains owner, the owner (and everything before it) is
// stripped. Otherwise the last path segment becomes the identifier and the
// rest is joined under account as the owner.
func parseIdentifierOwner(account, owner, identifier string) (string, string) {
	if owner != "" && strings.Contains(identifier, owner) {
		idx := strings.Index(identifier, owner)
		identifier = identifier[idx+len(owner):]
		identifier = strings.TrimPrefix(identifier, "/")
	} else {
		var ownerDir string
		ownerDir, identifier = path.Split(identifier)
		ownerDir = strings.TrimSuffix(ownerDir, "/")
		owner = path.Join(account, ownerDir)
	}
	return identifier, owner
}

// uploadTargetScope is the space path report upload and ensure-repo
// share. Harness Code ignores target_scope on the server and uses the owner
// path, which covclient.ResolveScope already applied; leave it empty so the two
// paths stay aligned.
func uploadTargetScope(params *uploadParams) string {
	if strings.EqualFold(params.provider, env.HarnessProvider) {
		return ""
	}
	return params.resolvedScope.SpacePath()
}

// signURLRequest is the presign request body. Field shapes are sent
// verbatim to the coverage server and must match its SignURLRequest.
type signURLRequest struct {
	TargetScope   string              `json:"target_scope,omitempty"`
	BaseURL       string              `json:"base_url"`
	Provider      string              `json:"provider"`
	Owner         string              `json:"owner"`
	Identifier    string              `json:"identifier"`
	CommitID      string              `json:"commit_id"`
	Branch        string              `json:"branch"`
	BaseBranch    string              `json:"base_branch"`
	DefaultBranch string              `json:"default_branch"`
	BuildID       int                 `json:"build_id,omitempty"`
	PipelineID    string              `json:"pipeline_id,omitempty"`
	ExecutionID   string              `json:"execution_id"`
	PullRequestID int64               `json:"pull_request_id,omitempty"`
	MergeBaseSHA  string              `json:"merge_base_sha,omitempty"`
	Tags          []string            `json:"tags,omitempty"`
	Version       int32               `json:"version"`
	Format        string              `json:"format"`
	SkippedKeys   []string            `json:"skipped_keys,omitempty"`
	DiffByFile    map[string][]uint32 `json:"diff_by_file,omitempty"`
}

type signURLResponse struct {
	ExternalID string `json:"external_id"`
	CreatedAt  int64  `json:"created_at"`
	Location   string `json:"location"`
	URL        string `json:"url"`
}

func uploadPath(accountID string) string {
	return fmt.Sprintf(covclient.APIPrefix+"/accounts/%s/reports/upload", accountID)
}

func completePath(accountID, externalID string) string {
	return fmt.Sprintf(covclient.APIPrefix+"/accounts/%s/reports/%s/complete", accountID, externalID)
}

// loadUploadExcludePatterns resolves the effective exclude patterns
// for report upload: coverage repository settings (even an empty value)
// replace cliPatterns entirely; see format.PatternsFromSettings.
func loadUploadExcludePatterns(
	ctx *cmdctx.Ctx, params *uploadParams, cliPatterns []string,
) ([]format.ExcludePattern, error) {
	repoID, err := covclient.EnsureRepo(ctx, params.resolvedScope, params.provider, params.owner, params.identifier)
	if err != nil {
		return nil, fmt.Errorf("resolve exclude settings repository: %w", err)
	}
	excludeText, err := covclient.ExcludeText(ctx, params.resolvedScope, repoID)
	if err != nil {
		return nil, fmt.Errorf("load coverage repository exclude settings: %w", err)
	}
	return format.PatternsFromSettings(excludeText, cliPatterns)
}

// upload is the `execute coverage:upload` orchestration: resolve
// parameters, discover coverage files, load server-side excludes once, then
// parse/encode/upload every file concurrently.
func upload(ctx *cmdctx.Ctx, f uploadFlags) error {
	params, err := resolveUploadParams(f)
	if err != nil {
		return err
	}

	lookupFiles, err := covfile.Lookup(f.files...)
	if err != nil {
		return fmt.Errorf("failed to lookup coverage file: %w", err)
	}

	excludePatterns, err := loadUploadExcludePatterns(ctx, params, f.excludePatterns)
	if err != nil {
		return err
	}

	pathResolver := env.NewLazyTreeFromGit()
	repoRef := path.Join(params.owner, params.identifier)

	var errGroup errgroup.Group
	for _, file := range lookupFiles {
		errGroup.Go(func() error {
			if err := processUploadFile(ctx, params, repoRef, file, excludePatterns, pathResolver); err != nil {
				return fmt.Errorf("failed to upload coverage file %s: %w", file.Path, err)
			}
			return nil
		})
	}
	return errGroup.Wait()
}

func processUploadFile(
	ctx *cmdctx.Ctx,
	params *uploadParams,
	repoRef string,
	file covfile.File,
	excludePatterns []format.ExcludePattern,
	pathResolver *env.Tree,
) error {
	hlog.Info("processing coverage file", "path", file.Path)
	defer hlog.Info("finished processing coverage file", "path", file.Path)

	baseRef := ""
	if params.base != "" {
		baseRef = env.EnsureRefFetched(params.base)
	}

	reportData, enc, err := buildReport(repoRef, file, baseRef, excludePatterns, pathResolver, params)
	if err != nil {
		return err
	}

	return uploadCoverageReport(ctx, params, baseRef, reportData, enc)
}

func buildReport(
	repoRef string,
	file covfile.File,
	baseRef string,
	excludePatterns []format.ExcludePattern,
	pathResolver *env.Tree,
	params *uploadParams,
) ([]byte, format.EncodeResult, error) {
	f, err := os.Open(file.Path)
	if err != nil {
		return nil, format.EncodeResult{}, fmt.Errorf("failed to open coverage file: %w", err)
	}
	defer func() { _ = f.Close() }()

	buf := new(bytes.Buffer)

	var writeOpts []format.WriteOption
	switch {
	case params.commitBefore != "":
		writeOpts = append(writeOpts, format.WithDiff(params.commitBefore, params.commitID))
	case baseRef != "":
		writeOpts = append(writeOpts, format.WithDiff(baseRef, params.commitID))
	}

	writeOpts = append(writeOpts, format.WithHintDirs(filepath.Dir(file.Path)))
	writeOpts = append(writeOpts, format.WithPathResolver(pathResolver))

	if params.patch {
		writeOpts = append(writeOpts, format.WithPatch(true))
	}
	if len(excludePatterns) > 0 {
		writeOpts = append(writeOpts, format.WithExcludePatterns(excludePatterns))
	}

	enc, err := format.Write(repoRef, file.Format, f, buf, writeOpts...)
	if err != nil {
		return nil, format.EncodeResult{}, fmt.Errorf("failed to parse coverage file: %w", err)
	}

	return buf.Bytes(), enc, nil
}

// uploadCoverageReport presigns a storage location for the encoded
// report, PUTs the bytes there, then marks the report complete. The storage
// PUT carries no Harness auth header: the presigned URL embeds its own.
func uploadCoverageReport(
	ctx *cmdctx.Ctx, params *uploadParams, baseRef string, reportData []byte, enc format.EncodeResult,
) error {
	urlResp, err := getPresignedURL(ctx, params, baseRef, enc)
	if err != nil {
		return fmt.Errorf("presign failed: %w", err)
	}

	hlog.Info("uploading coverage report")
	if err := putReportBytes(ctx.Context, urlResp.Location, reportData); err != nil {
		return fmt.Errorf("upload failed: %w", err)
	}

	hlog.Info("marking coverage report as complete")
	if err := completeReport(ctx, urlResp.ExternalID); err != nil {
		return fmt.Errorf("complete failed: %w", err)
	}

	return nil
}

func getPresignedURL(
	ctx *cmdctx.Ctx, params *uploadParams, baseRef string, enc format.EncodeResult,
) (signURLResponse, error) {
	var mergeBaseSHA string
	var err error
	switch {
	case params.commitBefore != "":
		mergeBaseSHA = params.commitBefore
	case baseRef != "" && params.branch != "" && params.base != params.branch:
		mergeBaseSHA, err = env.GetMergeBase(baseRef, params.branch)
		if err != nil {
			return signURLResponse{}, fmt.Errorf("failed to get merge base [%s, %s]: %w", baseRef, params.branch, err)
		}
	}

	req := signURLRequest{
		TargetScope:   uploadTargetScope(params),
		Provider:      params.provider,
		BaseURL:       params.baseURL,
		Owner:         params.owner,
		Identifier:    params.identifier,
		CommitID:      params.commitID,
		DefaultBranch: params.defaultBranch,
		Branch:        params.branch,
		BaseBranch:    params.base,
		BuildID:       params.buildID,
		PipelineID:    params.pipelineID,
		ExecutionID:   params.executionID,
		PullRequestID: params.pullReqID,
		MergeBaseSHA:  mergeBaseSHA,
		Tags:          params.tags,
		Version:       1,
		Format:        enc.Format,
		SkippedKeys:   enc.SkippedKeys,
		DiffByFile:    enc.DiffByFile,
	}

	accountID := ctx.Auth.AccountID
	raw, _, err := client.New(ctx).DoRequest(client.Request{
		Method: "POST",
		Path:   uploadPath(accountID),
		QueryParams: map[string]string{
			"routingId": accountID,
			"accountId": accountID,
		},
		Body:        req,
		NoAccountID: true,
	})
	if err != nil {
		return signURLResponse{}, err
	}
	var resp signURLResponse
	if err := covclient.DecodeInto(raw, &resp); err != nil {
		return signURLResponse{}, err
	}
	return resp, nil
}

// putReportBytes uploads the encoded report directly to the
// presigned storage location. This is deliberately a bare HTTP call, not
// client.Client: the presigned URL is not a Harness host and must carry no
// Harness auth header — authorization is already embedded in the URL.
func putReportBytes(ctx context.Context, location string, data []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, location, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("building storage upload request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("uploading coverage report: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("storage upload returned status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

func completeReport(ctx *cmdctx.Ctx, externalID string) error {
	accountID := ctx.Auth.AccountID
	_, _, err := client.New(ctx).DoRequest(client.Request{
		Method: "PUT",
		Path:   completePath(accountID, externalID),
		QueryParams: map[string]string{
			"routingId": accountID,
			"accountId": accountID,
		},
		NoAccountID: true,
	})
	return err
}
