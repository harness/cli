// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"io"
	"os"
	"strings"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/harness/cli/v3/pkg/hlog"
)

type hlogWriter struct{}

func (hlogWriter) Write(p []byte) (int, error) {
	hlog.Debug(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

// migrationLogWriter sends every migration engine log line to hlog (visible with
// --debug or HARNESS_CLI_LOGFILE) and also prints error-level lines to stderr,
// since a dry run reports enumeration failures nowhere else.
type migrationLogWriter struct {
	debug, stderr io.Writer
}

func (w migrationLogWriter) Write(p []byte) (int, error) {
	return w.debug.Write(p)
}

func (w migrationLogWriter) WriteLevel(level zerolog.Level, p []byte) (int, error) {
	if level >= zerolog.ErrorLevel {
		_, _ = w.stderr.Write(p)
	}
	return w.debug.Write(p)
}

func routeMigrationLogsToHlog() {
	console := func(out io.Writer) io.Writer {
		return zerolog.ConsoleWriter{Out: out, NoColor: true, PartsExclude: []string{zerolog.TimestampFieldName}}
	}
	w := migrationLogWriter{debug: console(hlogWriter{}), stderr: console(os.Stderr)}
	log.Logger = zerolog.New(w).With().Timestamp().Logger()
}
