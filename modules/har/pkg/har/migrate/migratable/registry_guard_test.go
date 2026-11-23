package migratable

import (
	"testing"

	"github.com/harness/cli/modules/har/pkg/har/migrate/types"
)

func TestCountContentFiles(t *testing.T) {
	cases := []struct {
		name         string
		artifactType types.ArtifactType
		uris         []string
		want         int
	}{
		{"ruby index only", types.RUBY, []string{
			"/specs.4.8.gz", "/latest_specs.4.8.gz", "/prerelease_specs.4.8.gz",
			"/quick/Marshal.4.8/rails-7.0.0.gemspec.rz",
		}, 0},
		{"ruby gem counts", types.RUBY, []string{"/specs.4.8.gz", "/gems/rails-7.0.0.gem"}, 1},
		{"ruby on a conda repo still counts", types.RUBY, []string{"/linux-64/numpy-1.0-0.conda"}, 1},
		{"cran index only", types.CRAN, []string{"/src/contrib/PACKAGES", "/src/contrib/PACKAGES.gz"}, 0},
		{"cran package counts", types.CRAN, []string{"/src/contrib/PACKAGES", "/src/contrib/jsonlite_1.8.0.tar.gz"}, 1},
		{"python index only", types.PYTHON, []string{"/.pypi/simple/requests/index.html"}, 0},
		{"other types count every file", types.PUPPET, []string{"/specs.4.8.gz", "/src/contrib/PACKAGES"}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files := []types.File{{Uri: "/folder", Folder: true}}
			for _, u := range tc.uris {
				files = append(files, types.File{Uri: u})
			}
			if got := countContentFiles(files, tc.artifactType); got != tc.want {
				t.Errorf("countContentFiles = %d, want %d", got, tc.want)
			}
		})
	}
}
