package types

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/pterm/pterm"
	"github.com/rs/zerolog/log"
	"go.yaml.in/yaml/v3"
)

type RegistryType string

var (
	HAR        RegistryType = "HAR"
	JFROG      RegistryType = "JFROG"
	MOCK_JFROG RegistryType = "MOCK_JFROG"
	NEXUS      RegistryType = "NEXUS"
	HARBOR     RegistryType = "HARBOR"
)

type ArtifactType string

var (
	DOCKER      ArtifactType = "DOCKER"
	HELM        ArtifactType = "HELM"
	HELM_LEGACY ArtifactType = "HELM_LEGACY"
	GENERIC     ArtifactType = "GENERIC"
	PYTHON      ArtifactType = "PYTHON"
	MAVEN       ArtifactType = "MAVEN"
	NPM         ArtifactType = "NPM"
	NUGET       ArtifactType = "NUGET"
	RPM         ArtifactType = "RPM"
	GO          ArtifactType = "GO"
	CONDA       ArtifactType = "CONDA"
	COMPOSER    ArtifactType = "COMPOSER"
	DART        ArtifactType = "DART"
	RAW         ArtifactType = "RAW"
	SWIFT       ArtifactType = "SWIFT"
	DEBIAN      ArtifactType = "DEBIAN"
	CONAN       ArtifactType = "CONAN"
	PUPPET      ArtifactType = "PUPPET"
	HELM_HTTP   ArtifactType = "HELM_HTTP"
	RUBY        ArtifactType = "RUBY"
	CRAN        ArtifactType = "CRAN"
	TERRAFORM   ArtifactType = "TERRAFORM"
)

// knownArtifactTypesList is the exhaustive, ordered list of valid ArtifactType
// values and the SINGLE SOURCE OF TRUTH for "which types exist": config
// validation and the validation error message are both derived from it. Add
// new types here (and to the var block above) whenever a new ArtifactType is
// introduced.
var knownArtifactTypesList = []ArtifactType{
	DOCKER, HELM, HELM_LEGACY, GENERIC, PYTHON, MAVEN, NPM, NUGET,
	RPM, GO, CONDA, COMPOSER, DART, RAW, SWIFT, DEBIAN, CONAN, PUPPET, HELM_HTTP, RUBY, CRAN,
	TERRAFORM,
}

// knownArtifactTypes is the lookup set derived from knownArtifactTypesList.
var knownArtifactTypes = func() map[ArtifactType]struct{} {
	m := make(map[ArtifactType]struct{}, len(knownArtifactTypesList))
	for _, t := range knownArtifactTypesList {
		m[t] = struct{}{}
	}
	return m
}()

// KnownArtifactTypes returns the ordered list of all valid ArtifactType
// values. Safe to mutate by the caller (a fresh copy is returned each time).
func KnownArtifactTypes() []ArtifactType {
	out := make([]ArtifactType, len(knownArtifactTypesList))
	copy(out, knownArtifactTypesList)
	return out
}

// KnownArtifactTypesString returns the valid types as a single
// comma-separated string for error messages and help text.
func KnownArtifactTypesString() string {
	parts := make([]string, len(knownArtifactTypesList))
	for i, t := range knownArtifactTypesList {
		parts[i] = string(t)
	}
	return strings.Join(parts, ", ")
}

// IsKnownArtifactType reports whether t is a recognised ArtifactType value.
func IsKnownArtifactType(t ArtifactType) bool {
	_, ok := knownArtifactTypes[t]
	return ok
}

// Config represents the top-level configuration structure
type Config struct {
	Version     string            `yaml:"version"`
	Concurrency int               `yaml:"concurrency"`
	Overwrite   bool              `yaml:"overwrite"`
	DryRun      bool              `yaml:"dryRun"`
	// Summary, when true, prints condensed per-status counts instead of the
	// full per-file table.
	Summary bool `yaml:"summary"`
	// ResultFile, when set, is a path to write one JSON-lines record per
	// per-coordinate result (types.FileStat) for automation to consume.
	ResultFile string `yaml:"resultFile,omitempty"`
	Source      RegistryConfig    `yaml:"source"`
	Dest        RegistryConfig    `yaml:"destination"`
	Mappings    []RegistryMapping `yaml:"mappings"`
}

// RegistryConfig defines the source ar configuration
type RegistryConfig struct {
	Endpoint    string            `yaml:"endpoint"`
	Type        RegistryType      `yaml:"type"`
	Credentials CredentialsConfig `yaml:"credentials,omitempty"`
	Insecure    bool              `yaml:"insecure" default:"false"`
	// Runtime fields — not in YAML, set by the handler from auth context
	AccountID  string `yaml:"-"`
	APIBaseURL string `yaml:"-"`
}

type DateFilterMatch string

const (
	DateFilterMatchAny DateFilterMatch = "ANY"
	DateFilterMatchAll DateFilterMatch = "ALL"
)

// DateFilter defines time-based filtering criteria for a registry mapping.
// Files are included when their creation or download timestamp satisfies the
// configured thresholds, combined via Match ANY/ALL logic.
type DateFilter struct {
	Match           DateFilterMatch `yaml:"match"`
	CreatedAfter    *time.Time      `yaml:"createdAfter"`
	DownloadedAfter *time.Time      `yaml:"downloadedAfter"`
}

// RegistryMapping defines the mapping between source and destination registries
// Slashes are used to defined the scope. The format would be
// - "registry": Create registry at Account level
// - "org/registry": Create registry at Org level
// - "org/project/registry": Create registry at Project level
type RegistryMapping struct {
	ArtifactType        ArtifactType `yaml:"artifactType"`
	SourceRegistry      string       `yaml:"sourceRegistry"`
	DestinationRegistry string       `yaml:"destinationRegistry"`
	// NOT IMPLEMENTED YET
	IncludePatterns []string `yaml:"includePatterns"`
	ExcludePatterns []string `yaml:"excludePatterns"`
	//Optional
	SourcePackageHostname string            `yaml:"sourcePackageHostname"`
	DateFilter            *DateFilter       `yaml:"dateFilter"`
	PackageFilters        []PackageSelector `yaml:"packageFilters,omitempty"`
}

// CredentialsConfig defines the credential configuration
type CredentialsConfig struct {
	Username string `yaml:"username,omitempty"`
	Password string `yaml:"password,omitempty"`
}

// LoadConfig loads the configuration from a file
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("error reading config file: %w", err)
	}

	// Expand environment variables in the file
	expandedData := expandEnvInYaml(string(data))

	var config Config
	if err := yaml.Unmarshal([]byte(expandedData), &config); err != nil {
		return nil, fmt.Errorf("error parsing config file: %w", err)
	}

	// Validate the configuration
	if err := validateConfig(&config); err != nil {
		return nil, err
	}

	return &config, nil
}

// expandEnvInYaml expands environment variables in YAML content
func expandEnvInYaml(content string) string {
	// Process ${VAR} style environment variables
	result := os.Expand(content, func(key string) string {
		return os.Getenv(key)
	})

	return result
}

// validateConfig performs basic validation on the configuration
func validateConfig(config *Config) error {
	// Check migration configuration
	if config.Concurrency <= 0 {
		return fmt.Errorf("concurrency must be greater than 0")
	}

	// Validate source and destination registry configurations
	if err := validateCredentials(config.Source); err != nil {
		return fmt.Errorf("invalid source credentials block provided in config: %w", err)
	}

	if err := validateCredentials(config.Dest); err != nil {
		return fmt.Errorf("invalid destination credentials block provided in config: %w", err)
	}

	// Validate registry mappings
	if len(config.Mappings) == 0 {
		return fmt.Errorf("at least one registry mapping must be defined")
	}
	// Validate each mapping
	for i, mapping := range config.Mappings {
		if mapping.SourceRegistry == "" {
			return fmt.Errorf("mapping %d: source registry cannot be empty", i)
		}
		if mapping.DestinationRegistry == "" {
			return fmt.Errorf("mapping %d: destination registry cannot be empty", i)
		}
		if !IsKnownArtifactType(mapping.ArtifactType) {
			return fmt.Errorf("mapping %d: unknown artifactType %q — valid values are: %s", i, mapping.ArtifactType, KnownArtifactTypesString())
		}
		if err := ValidatePackageFilters(mapping.PackageFilters, mapping.ArtifactType); err != nil {
			return fmt.Errorf("mapping %d: %w", i, err)
		}
		if mapping.ArtifactType == MAVEN && mapping.DateFilter != nil {
			msg := fmt.Sprintf("mapping %d: date filter is enabled for %s — maven-metadata.xml may not be in sync with the migrated artifacts", i, MAVEN)
			log.Warn().Msg(msg)
			pterm.Warning.Println(msg)
		}
	}

	return nil
}

func validateCredentials(registry RegistryConfig) error {
	// Check that the endpoint is not empty
	if registry.Endpoint == "" {
		return fmt.Errorf("registry endpoint cannot be empty")
	}

	// Validate registry type
	if registry.Type == "" {
		return fmt.Errorf("registry type cannot be empty")
	}

	// Check supported registry types
	switch registry.Type {
	case HAR, JFROG, NEXUS, MOCK_JFROG, HARBOR:
		// These are supported
	default:
		return fmt.Errorf("unsupported registry type: %s", registry.Type)
	}

	// Validate credentials
	// Authentication must be provided via either token or username
	hasUsername := registry.Credentials.Username != ""
	hasToken := registry.Credentials.Password != ""

	if !hasToken && !hasUsername {
		return fmt.Errorf("either token or username must be provided for authentication")
	}

	if hasUsername && registry.Credentials.Password == "" {
		return fmt.Errorf("password must be provided when using username authentication")
	}

	return nil
}
