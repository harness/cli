package util

import (
	"fmt"
	"os"
	"strings"

	"github.com/harness/cli/modules/har/pkg/har/migrate/types"

	"github.com/pterm/pterm"
)

func GenOCIImagePath(host string, pathParams ...string) string {
	params := strings.Join(pathParams, "/")
	return fmt.Sprintf("%s/%s", host, params)
}

func GetRegistryRef(account string, ref string, registry string) string {
	result := []string{account}
	ref = strings.TrimSuffix(ref, "/")
	ref = strings.TrimPrefix(ref, "/")
	registry = strings.TrimPrefix(registry, "/")
	registry = strings.TrimSuffix(registry, "/")
	if ref != "" {
		result = append(result, ref)
	}
	result = append(result, registry)
	return strings.Join(result, "/")
}

func GetSkipPrinter() *pterm.PrefixPrinter {
	return &pterm.PrefixPrinter{
		MessageStyle: &pterm.ThemeDefault.WarningMessageStyle,
		Prefix: pterm.Prefix{
			Style: &pterm.ThemeDefault.WarningPrefixStyle,
			Text:  "SKIPPED",
		},
		Writer: os.Stdout,
	}
}

// AddPackageErrorToStat records a package-level failure (e.g. enumeration
// errors that never reach the per-file job loop) in the transfer stats so it
// shows up in the migration summary instead of being silently dropped.
func AddPackageErrorToStat(stats *types.TransferStats, pkg types.Package, srcRegistry string, err error) {
	stat := types.FileStat{
		Name:     pkg.Name,
		Registry: srcRegistry,
		Uri:      pkg.URL,
		Size:     int64(pkg.Size),
		Status:   types.StatusFail,
		Error:    err.Error(),
	}
	stats.Add(stat)
}

func AddFileErrorToStat(stats *types.TransferStats, file *types.File, srcRegistry string, err error) {
	stats.Add(types.FileStat{
		Name:     file.Name,
		Registry: srcRegistry,
		Uri:      file.Uri,
		Size:     int64(file.Size),
		Status:   types.StatusFail,
		Error:    err.Error(),
	})
}

// AddRegistryErrorToStat records a failure that aborts a whole registry, so it is
// counted in the Failed total instead of only surfacing as an engine error.
func AddRegistryErrorToStat(stats *types.TransferStats, srcRegistry string, err error) {
	stats.Add(types.FileStat{
		Name:     srcRegistry,
		Registry: srcRegistry,
		Status:   types.StatusFail,
		Error:    err.Error(),
	})
}
