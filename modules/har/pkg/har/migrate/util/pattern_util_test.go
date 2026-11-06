package util

import (
	"testing"

	"github.com/harness/cli/modules/har/pkg/har/migrate/types"
)

func TestFilterableArtifactMatchesConfigValidation(t *testing.T) {
	for _, at := range types.KnownArtifactTypes() {
		if got, want := IsFileLevelFilterableArtifact(at), types.IsFileLevelPatternFilterable(at); got != want {
			t.Errorf("%s: engine file-level filtering = %v, config validation = %v", at, got, want)
		}
		if got, want := IsPackageLevelFilterableArtifact(at), types.IsPackageLevelPatternFilterable(at); got != want {
			t.Errorf("%s: engine package-level filtering = %v, config validation = %v", at, got, want)
		}
	}
	if !IsFileLevelFilterableArtifact(types.PUPPET) || !IsPackageLevelFilterableArtifact(types.DEBIAN) {
		t.Error("PUPPET patterns must filter files and DEBIAN patterns must filter packages")
	}
}
