package migrate

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/harness/cli/modules/har/pkg/har/migrate/adapter"
	"github.com/harness/cli/modules/har/pkg/har/migrate/engine"
	"github.com/harness/cli/modules/har/pkg/har/migrate/migratable"
	"github.com/harness/cli/modules/har/pkg/har/migrate/types"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	_ "github.com/harness/cli/modules/har/pkg/har/migrate/adapter/har"
	_ "github.com/harness/cli/modules/har/pkg/har/migrate/adapter/jfrog"
	_ "github.com/harness/cli/modules/har/pkg/har/migrate/adapter/nexus"
)

// MigrationService handles the migration process
type MigrationService struct {
	config      *types.Config
	source      adapter.Adapter
	destination adapter.Adapter
	dryRunStats *types.DryRunStats
}

// NewMigrationService creates a new migration service
func NewMigrationService(ctx context.Context, cfg *types.Config) (*MigrationService, error) {
	sourceAdapter, err := adapter.GetAdapter(ctx, cfg.Source)
	if err != nil {
		return nil, fmt.Errorf("failed to get source adapter: %v", err)
	}
	destAdapter, err := adapter.GetAdapter(ctx, cfg.Dest)
	if err != nil {
		return nil, fmt.Errorf("failed to get destination adapter: %v", err)
	}

	svc := &MigrationService{
		config:      cfg,
		source:      sourceAdapter,
		destination: destAdapter,
	}

	if cfg.DryRun {
		svc.dryRunStats = &types.DryRunStats{
			Files:       make([]types.DryRunFileEntry, 0),
			Directories: make(map[string]*types.DryRunDirectoryEntry),
		}
	}

	return svc, nil
}

// Run executes the migration process
func (m *MigrationService) Run(ctx context.Context) error {
	logger := log.With().
		Str("source_type", string(m.config.Source.Type)).
		Str("destination_type", string(m.config.Dest.Type)).
		Logger()

	logger.Info().Msg("Starting migration process")

	var jobs []engine.Job
	var transferStats types.TransferStats
	transferStats.FileStats = make([]types.FileStat, 0)

	for _, mapping := range m.config.Mappings {
		job := migratable.NewRegistryJob(m.source, m.destination, mapping.SourceRegistry, mapping.SourcePackageHostname,
			mapping.DestinationRegistry, mapping.ArtifactType, &transferStats, &mapping, m.config, m.dryRunStats)
		jobs = append(jobs, job)
	}

	eng := engine.NewEngine(m.config.Concurrency, jobs)
	err := eng.Execute(ctx)
	if err != nil {
		logger.Error().Err(err).Msgf("Engine execution saw following errors: %v", err)
	}
	logger.Info().Msg("Migration process completed")

	if m.config.DryRun {
		return m.writeDryRunOutput(logger)
	}

	printFileStats(transferStats.FileStats)

	if jsonData, err := json.MarshalIndent(transferStats.FileStats, "", "  "); err == nil {
		logger.Info().RawJSON("file_stats", jsonData).Int("total_files", len(transferStats.FileStats)).Msg("Migration file statistics")
	}

	return nil
}

func printFileStats(stats []types.FileStat) {
	tw := table.NewWriter()
	tw.SetOutputMirror(os.Stdout)
	tw.AppendHeader(table.Row{"Name", "Registry", "Size", "Status", "Error"})
	for _, s := range stats {
		tw.AppendRow(table.Row{s.Name, s.Registry, s.Size, string(s.Status), s.Error})
	}
	tw.Render()
}

func (m *MigrationService) writeDryRunOutput(logger zerolog.Logger) error {
	timestamp := time.Now().Format("20060102_150405")
	outputDir := "dry-run-output"
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	fileListPath := filepath.Join(outputDir, fmt.Sprintf("file_list_%s.json", timestamp))
	fileListData, err := json.MarshalIndent(m.dryRunStats.Files, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal file list: %w", err)
	}
	if err := os.WriteFile(fileListPath, fileListData, 0644); err != nil {
		return fmt.Errorf("failed to write file list: %w", err)
	}
	logger.Info().Str("path", fileListPath).Int("total_files", len(m.dryRunStats.Files)).Msg("File list written")

	dirStructPath := filepath.Join(outputDir, fmt.Sprintf("directory_structure_%s.json", timestamp))
	dirStructData, err := json.MarshalIndent(m.dryRunStats.Directories, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal directory structure: %w", err)
	}
	if err := os.WriteFile(dirStructPath, dirStructData, 0644); err != nil {
		return fmt.Errorf("failed to write directory structure: %w", err)
	}
	logger.Info().Str("path", dirStructPath).Int("total_registries", len(m.dryRunStats.Directories)).Msg("Directory structure written")

	fmt.Printf("\n=== Dry Run Complete ===\n")
	fmt.Printf("Total files found: %d\n", len(m.dryRunStats.Files))
	fmt.Printf("File list written to: %s\n", fileListPath)
	fmt.Printf("Directory structure written to: %s\n", dirStructPath)

	return nil
}
