package migratable

import (
	"context"
	"fmt"
	"time"

	"github.com/harness/cli/modules/har/pkg/har/migrate/adapter"
	"github.com/harness/cli/modules/har/pkg/har/migrate/engine"
	"github.com/harness/cli/modules/har/pkg/har/migrate/tree"
	"github.com/harness/cli/modules/har/pkg/har/migrate/types"
	"github.com/harness/cli/modules/har/pkg/har/migrate/util"

	"github.com/google/uuid"
	"github.com/pterm/pterm"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

type Registry struct {
	srcRegistry           string
	sourcePackageHostname string
	destRegistry          string
	srcAdapter            adapter.Adapter
	destAdapter           adapter.Adapter
	artifactType          types.ArtifactType
	logger                zerolog.Logger
	stats                 *types.TransferStats
	mapping               *types.RegistryMapping
	config                *types.Config
	dryRunStats           *types.DryRunStats

	// Transient
	registry types.RegistryInfo
}

func NewRegistryJob(
	src adapter.Adapter,
	dest adapter.Adapter,
	srcRegistry string,
	sourcePackageHostname string,
	destRegistry string,
	artifactType types.ArtifactType,
	stats *types.TransferStats,
	mapping *types.RegistryMapping,
	config *types.Config,
	dryRunStats *types.DryRunStats,
) engine.Job {
	jobID := uuid.New().String()

	jobLogger := log.With().
		Str("job_type", "registry").
		Str("job_id", jobID).
		Str("source_registry", srcRegistry).
		Str("dest_registry", destRegistry).
		Logger()

	return &Registry{
		srcRegistry:           srcRegistry,
		sourcePackageHostname: sourcePackageHostname,
		destRegistry:          destRegistry,
		srcAdapter:            src,
		destAdapter:           dest,
		artifactType:          artifactType,
		logger:                jobLogger,
		stats:                 stats,
		mapping:               mapping,
		config:                config,
		dryRunStats:           dryRunStats,
	}
}

func (r *Registry) Info() string {
	return r.srcRegistry + ":" + r.destRegistry
}

// Pre Create registry at destination if it doesn't exist
func (r *Registry) Pre(ctx context.Context) error {
	// Extract trace ID from context if available
	traceID, _ := ctx.Value("trace_id").(string)
	logger := r.logger.With().
		Str("step", "pre").
		Str("trace_id", traceID).
		Logger()

	logger.Info().Msg("Starting registry pre-migration step")

	startTime := time.Now()

	// Skip destination registry check in dry-run mode
	if r.config.DryRun {
		logger.Info().Msg("Dry-run mode: skipping destination registry check")
		r.registry = types.RegistryInfo{
			Path: r.destRegistry,
		}
		logger.Info().
			Dur("duration", time.Since(startTime)).
			Msg("Completed registry pre-migration step (dry-run)")
		return nil
	}

	registry, err := r.destAdapter.GetRegistry(ctx, r.destRegistry)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to get registry %q", r.destRegistry)
		return fmt.Errorf("failed to get registry %q", r.destRegistry)
	}

	log.Info().Ctx(ctx).Msgf("Found registry %+v", registry)
	r.registry = registry

	logger.Info().
		Dur("duration", time.Since(startTime)).
		Msg("Completed registry pre-migration step")
	return nil
}

// Migrate Create down stream packages and migrate them
func (r *Registry) Migrate(ctx context.Context) error {
	traceID, _ := ctx.Value("trace_id").(string)
	logger := r.logger.With().
		Str("step", "migrate").
		Str("trace_id", traceID).
		Logger()

	logger.Info().Msg("Starting registry migration step")

	if len(r.mapping.IncludePatterns) > 0 && len(r.mapping.ExcludePatterns) > 0 {
		logger.Error().Msgf("Either include or Exclude Pattern is suppoted at a time for %s", r.artifactType)
		return fmt.Errorf("failed in validating config file for %s ", r.artifactType)
	}

	startTime := time.Now()

	files, err2 := r.srcAdapter.GetFiles(r.srcRegistry)
	if err2 != nil {
		logger.Error().Msgf("Failed to get files from registry %s", r.srcRegistry)
		return fmt.Errorf("get files from registry %s failed: %w", r.srcRegistry, err2)
	}

	pterm.Info.Println(fmt.Sprintf("Pulled %d file(s) from registry %s", len(files), r.srcRegistry))

	// Keep a copy of the original file list before any filtering. This is used
	// to build an unfilteredRoot for PYTHON so version enumeration is not broken
	// when date filtering prunes .pypi index files.
	originalFiles := files

	// In dry-run mode, collect all files using thread-safe methods.
	if r.config.DryRun && r.dryRunStats != nil {
		var entries []types.DryRunFileEntry
		for _, file := range files {
			entries = append(entries, types.DryRunFileEntry{
				Registry:     r.srcRegistry,
				Name:         file.Name,
				Uri:          file.Uri,
				Size:         file.Size,
				LastModified: file.LastModified,
			})
		}
		r.dryRunStats.AddFiles(entries...)
		r.dryRunStats.EnsureRegistry(r.srcRegistry)
		logger.Info().Msgf("Dry-run: collected %d files from registry %s", len(files), r.srcRegistry)
	}

	// Date-based filtering: query the source for file timestamps and narrow the
	// file list to files that satisfy the configured createdAfter/downloadedAfter
	// thresholds. Index/metadata files (PyPI .pypi/) are always preserved so
	// package enumeration is not broken.
	currArtifactType := r.artifactType
	var dateFilteredFiles []types.File
	dateFilterActive := false

	if util.IsTimeBasedFilterPresent(r.mapping) {
		dateFilterActive = true
		df := r.mapping.DateFilter
		if err := util.ValidateDateFilter(df); err != nil {
			logger.Error().Err(err).Msg("Date filter validation failed")
			return err
		}

		searchedFiles, searchErr := r.srcAdapter.SearchFiles(r.srcRegistry)
		if searchErr != nil {
			logger.Error().Msgf("Failed to search files from registry %s", r.srcRegistry)
			return fmt.Errorf("search files from registry %s failed: %w", r.srcRegistry, searchErr)
		}
		logger.Info().Msgf("Applying time based filter (match: %s)", df.Match)
		filteredURIs := util.CreateMapOfFilteredFile(searchedFiles, r.mapping)
		logger.Info().Msgf("Time-based filter includes %d file(s) out of %d", len(filteredURIs), len(searchedFiles))

		// Preserve index/metadata files regardless of date — enumeration reads them.
		indexCount := 0
		for _, f := range files {
			if util.IsPackageIndexFile(r.artifactType, f.Uri) {
				if _, ok := filteredURIs[f.Uri]; !ok {
					filteredURIs[f.Uri] = struct{}{}
					indexCount++
				}
			}
		}
		if indexCount > 0 {
			logger.Info().Msgf("Preserving %d index/metadata file(s) exempt from date filter", indexCount)
		}

		dateFilteredFiles = util.FilterFilesByDate(files, filteredURIs)
		logger.Info().Msgf("Count of filtered files by date filter: %d -> %d", len(files), len(dateFilteredFiles))
		skippedByFilter := len(files) - len(dateFilteredFiles)
		pterm.Info.Println(fmt.Sprintf("Registry %s: %d file(s) pulled, %d under skip condition (date/pattern filters)",
			r.srcRegistry, len(files), skippedByFilter))

		// Narrow the tree for all types EXCEPT metadata-driven types (RPM, DEBIAN)
		// which need the full file tree to read repomd.xml / Packages.gz metadata.
		if !util.IsMetadataDrivenArtifact(currArtifactType) {
			files = dateFilteredFiles
		}
	}

	// Filter files based on include/exclude patterns
	if util.IsFileLevelFilterableArtifact(currArtifactType) {
		if len(r.mapping.IncludePatterns) > 0 || len(r.mapping.ExcludePatterns) > 0 {
			originalCount := len(files)
			filteredFiles := util.FilterFilesByPatterns(files, r.mapping.IncludePatterns, r.mapping.ExcludePatterns)
			files = filteredFiles
			logger.Info().Msgf("Filtered files: %d -> %d (includePatterns: %v, excludePatterns: %v)",
				originalCount, len(files), r.mapping.IncludePatterns, r.mapping.ExcludePatterns)
		}
	}

	root := tree.TransformToTree(files)

	// For PYTHON (IsAtomicVersionArtifact), build an unfilteredRoot from the
	// original file list so version.go can recover distributions that were pruned
	// by date or pattern filters. Other types pass nil.
	var unfilteredRoot *types.TreeNode
	if dateFilterActive && util.IsAtomicVersionArtifact(currArtifactType) {
		recoveryFiles := originalFiles
		if util.IsFileLevelFilterableArtifact(currArtifactType) &&
			(len(r.mapping.IncludePatterns) > 0 || len(r.mapping.ExcludePatterns) > 0) {
			recoveryFiles = util.FilterFilesByPatterns(originalFiles, r.mapping.IncludePatterns, r.mapping.ExcludePatterns)
		}
		unfilteredRoot = tree.TransformToTree(recoveryFiles)
	}

	pkgs, err := r.srcAdapter.GetPackages(r.srcRegistry, r.artifactType, root)
	if err != nil {
		logger.Error().Msg("Failed to get packages")
		return fmt.Errorf("get packages failed: %w", err)
	}

	// For metadata-driven types, re-apply date filter at the package level.
	if dateFilterActive && util.IsMetadataDrivenArtifact(currArtifactType) {
		originalPkgCount := len(pkgs)
		pkgs = util.FilterPackagesByFileName(pkgs, dateFilteredFiles)
		logger.Info().Msgf("Date filter (post-GetPackages): %d -> %d packages for %s",
			originalPkgCount, len(pkgs), currArtifactType)
	}

	// applying package level filter
	if util.IsPackageLevelFilterableArtifact(currArtifactType) {
		if len(r.mapping.IncludePatterns) > 0 || len(r.mapping.ExcludePatterns) > 0 {
			originalCount := len(pkgs)
			filteredPackages := util.FilterFilesByPatternsPackageName(pkgs, r.mapping.IncludePatterns, r.mapping.ExcludePatterns)
			pkgs = filteredPackages
			logger.Info().Msgf("Filtered packages: %d -> %d (includePatterns: %v, excludePatterns: %v)",
				originalCount, len(pkgs), r.mapping.IncludePatterns, r.mapping.ExcludePatterns)
		}
	}

	var jobs []engine.Job
	for _, pkg := range pkgs {
		treeNode, err2 := tree.GetNodeForPath(root, pkg.Path)
		if err2 != nil {
			logger.Error().Msgf("Failed to get node for path %s", pkg.Path)
			return fmt.Errorf("get node for path %s failed: %w", pkg.Path, err2)
		}
		job := NewPackageJob(r.srcAdapter, r.destAdapter, r.srcRegistry, r.sourcePackageHostname, r.destRegistry, r.artifactType, pkg, treeNode,
			r.stats, r.mapping, r.config, r.registry, r.dryRunStats, unfilteredRoot)
		jobs = append(jobs, job)
	}

	eng := engine.NewEngine(r.config.Concurrency, jobs)
	err = eng.Execute(ctx)
	if err != nil {
		logger.Error().Err(err).Msg("Engine execution saw following errors")
	}

	logger.Info().
		Dur("duration", time.Since(startTime)).
		Msg("Completed registry migration step")
	return nil
}

// Post Any post processing work
func (r *Registry) Post(ctx context.Context) error {
	traceID, _ := ctx.Value("trace_id").(string)
	logger := r.logger.With().
		Str("step", "post").
		Str("trace_id", traceID).
		Logger()

	logger.Info().Msg("Starting registry post-migration step")

	startTime := time.Now()
	// Your post-migration code here

	logger.Info().
		Dur("duration", time.Since(startTime)).
		Msg("Completed registry post-migration step")
	return nil
}
