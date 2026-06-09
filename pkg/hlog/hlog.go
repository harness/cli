// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package hlog

import (
	"log/slog"
	"os"

	"github.com/lmittmann/tint"
	"golang.org/x/term"
)

func newHandler(w *os.File, level slog.Level) slog.Handler {
	if term.IsTerminal(int(w.Fd())) {
		return tint.NewHandler(w, &tint.Options{
			Level:      level,
			TimeFormat: "15:04:05",
			ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
				if a.Value.Kind() == slog.KindAny {
					if _, ok := a.Value.Any().(error); ok {
						return tint.Attr(9, a)
					}
				}
				return a
			},
		})
	}
	return slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})
}

var logger = slog.New(slog.DiscardHandler)
var pluginName string

func applyPlugin(l *slog.Logger) *slog.Logger {
	if pluginName != "" {
		return l.With("plugin", pluginName)
	}
	return l
}

// SetDebug switches the logger to DEBUG level.
func SetDebug() {
	logger = applyPlugin(slog.New(newHandler(os.Stderr, slog.LevelDebug)))
}

// SetDebugFile opens path for append and switches the logger to DEBUG level writing to that file.
// If the file cannot be opened, the logger is left as-is (discard).
func SetDebugFile(path string) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	logger = applyPlugin(slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{Level: slog.LevelDebug})))
}

// SetPlugin records the plugin name and adds it as an attribute to every subsequent log line.
func SetPlugin(name string) {
	pluginName = name
	logger = logger.With("plugin", name)
}

func Debug(msg string, args ...any) { logger.Debug(msg, args...) }
func Info(msg string, args ...any)  { logger.Info(msg, args...) }
func Warn(msg string, args ...any)  { logger.Warn(msg, args...) }
func Error(msg string, args ...any) { logger.Error(msg, args...) }
