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

func TestValidateConfig_PatternsRejectedForNonFilterableType(t *testing.T) {
	// Every currently-known ArtifactType is classified as either file-level or
	// package-level filterable (that's the point of this fix), so exercise the
	// rejection branch with a type outside the known classification — this is
	// exactly the safety net the check exists for: a future artifact type added
	// without updating patterns.go must not silently ignore scope controls.
	unclassified := ArtifactType("UNCLASSIFIED_TYPE")
	mapping := validMapping(unclassified)
	mapping.IncludePatterns = []string{"*.txt"}

	cfg := &Config{
		Concurrency: 1,
		Source:      validCredentials(),
		Dest:        validCredentials(),
		Mappings:    []RegistryMapping{mapping},
	}

	err := validateConfig(cfg)
	if err == nil {
		t.Fatal("expected error for includePatterns on a non-pattern-filterable artifact type")
	}
	if !strings.Contains(err.Error(), "not supported for artifact type UNCLASSIFIED_TYPE") {
		t.Errorf("unexpected error message: %v", err)
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
