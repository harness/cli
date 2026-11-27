package types

import (
	"strings"
	"testing"
)

func validMapping(artifactType ArtifactType) RegistryMapping {
	return RegistryMapping{
		ArtifactType:        artifactType,
		SourceRegistry:      "src",
		DestinationRegistry: "dst",
	}
}

func validCredentials() RegistryConfig {
	return RegistryConfig{
		Endpoint: "https://example.com",
		Type:     HAR,
		Credentials: CredentialsConfig{
			Username: "user",
			Password: "pass",
		},
	}
}

// A new ArtifactType must be added to a pattern list in patterns.go; otherwise
// includePatterns/excludePatterns on it are rejected at config load.
func TestEveryKnownArtifactTypeIsPatternFilterable(t *testing.T) {
	for _, at := range KnownArtifactTypes() {
		if !IsPatternFilterable(at) {
			t.Errorf("%s is not classified as file-level or package-level pattern filterable", at)
		}
	}
}

func TestValidateConfig_PatternsAllowedForPuppet(t *testing.T) {
	mapping := validMapping(PUPPET)
	mapping.IncludePatterns = []string{"*.tar.gz"}

	cfg := &Config{
		Concurrency: 1,
		Source:      validCredentials(),
		Dest:        validCredentials(),
		Mappings:    []RegistryMapping{mapping},
	}

	if err := validateConfig(cfg); err != nil {
		t.Fatalf("expected PUPPET+includePatterns to be accepted (file-level filterable), got: %v", err)
	}
}

func TestValidateConfig_PatternsAllowedForDebian(t *testing.T) {
	mapping := validMapping(DEBIAN)
	mapping.ExcludePatterns = []string{"internal-*"}

	cfg := &Config{
		Concurrency: 1,
		Source:      validCredentials(),
		Dest:        validCredentials(),
		Mappings:    []RegistryMapping{mapping},
	}

	if err := validateConfig(cfg); err != nil {
		t.Fatalf("expected DEBIAN+excludePatterns to be accepted (package-level filterable), got: %v", err)
	}
}

func TestValidateConfig_DestEndpointNotRequired(t *testing.T) {
	dest := validCredentials()
	dest.Endpoint = ""

	cfg := &Config{
		Concurrency: 1,
		Source:      validCredentials(),
		Dest:        dest,
		Mappings:    []RegistryMapping{validMapping(GENERIC)},
	}

	if err := validateConfig(cfg); err != nil {
		t.Fatalf("expected empty destination.endpoint to be accepted (resolved later from auth/--pkg-url), got: %v", err)
	}
}

func TestValidateConfig_SourceEndpointStillRequired(t *testing.T) {
	src := validCredentials()
	src.Endpoint = ""

	cfg := &Config{
		Concurrency: 1,
		Source:      src,
		Dest:        validCredentials(),
		Mappings:    []RegistryMapping{validMapping(GENERIC)},
	}

	err := validateConfig(cfg)
	if err == nil {
		t.Fatal("expected error for empty source.endpoint")
	}
	if !strings.Contains(err.Error(), "endpoint cannot be empty") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidateConfig_IncludeAndExcludePatternsMutuallyExclusive(t *testing.T) {
	mapping := validMapping(GENERIC)
	mapping.IncludePatterns = []string{"*.txt"}
	mapping.ExcludePatterns = []string{"*.log"}

	cfg := &Config{
		Concurrency: 1,
		Source:      validCredentials(),
		Dest:        validCredentials(),
		Mappings:    []RegistryMapping{mapping},
	}

	err := validateConfig(cfg)
	if err == nil {
		t.Fatal("expected error when both includePatterns and excludePatterns are set")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidateConfig_BatchSize(t *testing.T) {
	cases := []struct {
		name       string
		sourceType RegistryType
		artifact   ArtifactType
		batchSize  int
		wantErr    string
	}{
		{"nexus maven", NEXUS, MAVEN, 500, ""},
		{"unset", HAR, DOCKER, 0, ""},
		{"negative", NEXUS, MAVEN, -1, "must not be negative"},
		{"other source", JFROG, MAVEN, 500, "only supported"},
		{"other artifact type", NEXUS, GENERIC, 500, "only supported"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mapping := validMapping(tc.artifact)
			mapping.BatchSize = tc.batchSize
			source := validCredentials()
			source.Type = tc.sourceType
			cfg := &Config{Concurrency: 1, Source: source, Dest: validCredentials(), Mappings: []RegistryMapping{mapping}}

			err := validateConfig(cfg)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("validateConfig: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("validateConfig error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestValidateConfig_ListConcurrency(t *testing.T) {
	cases := []struct {
		name        string
		sourceType  RegistryType
		artifact    ArtifactType
		concurrency int
		wantErr     string
	}{
		{"nexus maven", NEXUS, MAVEN, 16, ""},
		{"unset", HAR, DOCKER, 0, ""},
		{"negative", NEXUS, MAVEN, -1, "must not be negative"},
		{"too high", NEXUS, MAVEN, MaxListConcurrency + 1, "at most"},
		{"other source", JFROG, MAVEN, 16, "only supported"},
		{"other artifact type", NEXUS, NPM, 16, "only supported"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mapping := validMapping(tc.artifact)
			mapping.ListConcurrency = tc.concurrency
			source := validCredentials()
			source.Type = tc.sourceType
			cfg := &Config{Concurrency: 1, Source: source, Dest: validCredentials(), Mappings: []RegistryMapping{mapping}}

			err := validateConfig(cfg)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("validateConfig: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("validateConfig error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}
