package types

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// baseValidConfig returns a minimal Config that passes validateConfig, so tests
// can tweak a single field to exercise one rule at a time.
func baseValidConfig() *Config {
	cred := CredentialsConfig{Username: "user", Password: "pass"}
	return &Config{
		Concurrency: 1,
		Source:      RegistryConfig{Endpoint: "https://src.example", Type: JFROG, Credentials: cred},
		Dest:        RegistryConfig{Endpoint: "https://dst.example", Type: HAR, Credentials: cred},
		Mappings: []RegistryMapping{
			{
				ArtifactType:        MAVEN,
				SourceRegistry:      "src",
				DestinationRegistry: "dst",
			},
		},
	}
}

func TestValidateConfig_MavenWithDateFilterWarnsButPasses(t *testing.T) {
	config := baseValidConfig()
	after := time.Unix(0, 0)
	config.Mappings[0].DateFilter = &DateFilter{
		Match:        DateFilterMatchAny,
		CreatedAfter: &after,
	}

	if err := validateConfig(config); err != nil {
		t.Fatalf("expected MAVEN mapping with date filter to pass with a warning, got: %v", err)
	}
}

func TestValidateConfig_UnknownArtifactTypeRejected(t *testing.T) {
	config := baseValidConfig()
	config.Mappings[0].ArtifactType = ArtifactType("NOTAREALTYPE")

	err := validateConfig(config)
	if err == nil {
		t.Fatal("expected error for unknown artifactType, got nil")
	}
	if !strings.Contains(err.Error(), "NOTAREALTYPE") {
		t.Errorf("error should name the offending type, got: %v", err)
	}
}

func TestValidateConfig_AllKnownArtifactTypesAccepted(t *testing.T) {
	for _, at := range KnownArtifactTypes() {
		config := baseValidConfig()
		config.Mappings[0].ArtifactType = at
		if err := validateConfig(config); err != nil {
			t.Errorf("expected known artifactType %q to pass, got: %v", at, err)
		}
	}
}

func TestValidateConfig_EmptyArtifactTypeRejected(t *testing.T) {
	config := baseValidConfig()
	config.Mappings[0].ArtifactType = ArtifactType("")

	err := validateConfig(config)
	if err == nil {
		t.Fatal("expected error for empty artifactType, got nil")
	}
}

// TestLoadConfig_UnknownArtifactTypeFailsAtLoad verifies a typo'd artifactType
// fails at config LOAD time — i.e. before NewMigrationService and therefore
// before any source API call — and the error lists the valid values.
func TestLoadConfig_UnknownArtifactTypeFailsAtLoad(t *testing.T) {
	yaml := `
version: 1.0.0
concurrency: 1
source:
  endpoint: https://src.example
  type: JFROG
  credentials: {username: u, password: p}
destination:
  endpoint: https://dst.example
  type: HAR
  credentials: {username: u, password: p}
mappings:
  - artifactType: generic
    sourceRegistry: src
    destinationRegistry: dst
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected LoadConfig to reject lowercase artifactType, got nil")
	}
	if !strings.Contains(err.Error(), "generic") {
		t.Errorf("error should name the offending type, got: %v", err)
	}
	if !strings.Contains(err.Error(), "GENERIC") {
		t.Errorf("error should list valid values including GENERIC, got: %v", err)
	}
}

// TestKnownArtifactTypesSingleSource verifies the exported list and the lookup
// map stay in sync.
func TestKnownArtifactTypesSingleSource(t *testing.T) {
	list := KnownArtifactTypes()
	if len(list) != len(knownArtifactTypes) {
		t.Fatalf("KnownArtifactTypes() len %d != map len %d", len(list), len(knownArtifactTypes))
	}
	for _, at := range list {
		if !IsKnownArtifactType(at) {
			t.Errorf("KnownArtifactTypes() contains %q but IsKnownArtifactType says false", at)
		}
	}
}

// TestValidateConfig_PackageFiltersRejectedBeyondGranularity verifies §2:
// packageFilters requesting a granularity beyond what the artifact type
// supports is a config-load-time error, not a silent no-op.
func TestValidateConfig_PackageFiltersRejectedBeyondGranularity(t *testing.T) {
	config := baseValidConfig()
	config.Mappings[0].ArtifactType = DOCKER // package-only granularity
	config.Mappings[0].PackageFilters = []PackageSelector{
		{Package: "my-image", Versions: []string{"1.0.0"}},
	}

	err := validateConfig(config)
	if err == nil {
		t.Fatal("expected error for version-level packageFilters on DOCKER, got nil")
	}

	config = baseValidConfig()
	config.Mappings[0].ArtifactType = GO // version granularity, no file granularity
	config.Mappings[0].PackageFilters = []PackageSelector{
		{Package: "my-module", Files: []string{"v1.0.0.zip"}},
	}
	if err := validateConfig(config); err == nil {
		t.Fatal("expected error for file-level packageFilters on GO, got nil")
	}
}

func TestValidateConfig_PackageFiltersAcceptedWithinGranularity(t *testing.T) {
	config := baseValidConfig()
	config.Mappings[0].ArtifactType = NUGET // file granularity
	config.Mappings[0].PackageFilters = []PackageSelector{
		{Package: "my-package", Versions: []string{"1.0.0"}, Files: []string{"my-package.1.0.0.nupkg"}},
	}

	if err := validateConfig(config); err != nil {
		t.Fatalf("expected file-level packageFilters on NUGET to pass, got: %v", err)
	}
}

func TestValidateConfig_PackageFiltersEmptyPackageNameRejected(t *testing.T) {
	config := baseValidConfig()
	config.Mappings[0].PackageFilters = []PackageSelector{{Package: ""}}

	if err := validateConfig(config); err == nil {
		t.Fatal("expected error for empty package name in packageFilters, got nil")
	}
}
