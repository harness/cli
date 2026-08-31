// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

// Package console provides TTY-only interactive I/O utilities: prompts, secret
// input, and (eventually) formatted output, colors, and spinners.
// All functions require a real terminal; they return an error or zero value when
// stdout is not a TTY.
package console

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/term"
)

// ansiEscape matches CSI escape sequences: \x1b[ ... <letter>  (colors, bold, etc.)
var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// StripANSI removes ANSI/CSI escape sequences from s.
func StripANSI(s string) string { return ansiEscape.ReplaceAllString(s, "") }

// Color is an ANSI foreground color code.
type Color int

const (
	ColorBlack         Color = 30
	ColorRed           Color = 31
	ColorGreen         Color = 32
	ColorYellow        Color = 33
	ColorBlue          Color = 34
	ColorMagenta       Color = 35
	ColorCyan          Color = 36
	ColorWhite         Color = 37
	ColorBrightBlack   Color = 90
	ColorBrightRed     Color = 91
	ColorBrightGreen   Color = 92
	ColorBrightYellow  Color = 93
	ColorBrightBlue    Color = 94
	ColorBrightMagenta Color = 95
	ColorBrightCyan    Color = 96
	ColorBrightWhite   Color = 97
)

var (
	stdinTTYOnce  sync.Once
	stdinTTYIs    bool
	stdoutTTYOnce sync.Once
	stdoutTTYIs   bool
)

func ensureTTY() bool {
	stdinTTYOnce.Do(func() {
		stdinTTYIs = term.IsTerminal(int(syscall.Stdin))
	})
	return stdinTTYIs
}

func ensureStdoutTTY() bool {
	stdoutTTYOnce.Do(func() {
		stdoutTTYIs = term.IsTerminal(int(syscall.Stdout))
	})
	return stdoutTTYIs
}

func IsTTY() bool {
	return ensureTTY()
}

// IsStdoutTTY returns true when stdout is an interactive terminal.
func IsStdoutTTY() bool {
	return ensureStdoutTTY()
}

// IsBothTTY returns true when both stdin and stdout are interactive terminals.
// Use this before launching interactive TUI programs.
func IsBothTTY() bool {
	return ensureTTY() && ensureStdoutTTY()
}

// WithColor wraps text in ANSI color codes when stdout is a TTY.
func WithColor(c Color, text string) string {
	if !ensureStdoutTTY() {
		return text
	}
	return fmt.Sprintf("\x1b[%dm%s\x1b[0m", int(c), text)
}

// WithBoldColor wraps text in a bold ANSI code, additionally tinted with c when
// c != 0, when stdout is a TTY.
func WithBoldColor(c Color, text string) string {
	if !ensureStdoutTTY() {
		return text
	}
	if c != 0 {
		return fmt.Sprintf("\x1b[1;%dm%s\x1b[0m", int(c), text)
	}
	return fmt.Sprintf("\x1b[1m%s\x1b[0m", text)
}

// WithBold wraps text in a bold ANSI code (no color) when stdout is a TTY.
func WithBold(text string) string {
	if !ensureStdoutTTY() {
		return text
	}
	return fmt.Sprintf("\x1b[1m%s\x1b[0m", text)
}

// WithItalic wraps text in an italic ANSI code (no color) when stdout is a TTY.
func WithItalic(text string) string {
	if !ensureStdoutTTY() {
		return text
	}
	return fmt.Sprintf("\x1b[3m%s\x1b[0m", text)
}

// WithHighlight renders text in red on a dark grey background when stdout is
// a TTY, used for inline code spans.
func WithHighlight(text string) string {
	if !ensureStdoutTTY() {
		return text
	}
	return fmt.Sprintf("\x1b[91;48;5;236m%s\x1b[0m", text)
}

// ReadSecret prints prompt to stderr and reads a masked line from the terminal.
func ReadSecret(prompt string) (string, error) {
	if !ensureTTY() {
		return "", fmt.Errorf("console.ReadSecret requires a TTY")
	}
	fmt.Fprint(os.Stderr, prompt)
	b, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("reading secret: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

// GreenCheck returns a green ✓ when stdout is a TTY, or plain "✓" otherwise.
func GreenCheck() string {
	return WithColor(ColorGreen, "✓")
}

// RedX returns a red ✗ when stdout is a TTY, or plain "✗" otherwise.
func RedX() string {
	return WithColor(ColorRed, "✗")
}

// YellowWarning returns a yellow ⚠ when stdout is a TTY, or plain "⚠" otherwise.
func YellowWarning() string {
	return WithColor(ColorYellow, "⚠")
}

// PrintError prints msg to stderr. On a TTY the text is red; otherwise plain.
func PrintError(msg string) {
	if ensureTTY() {
		fmt.Fprintf(os.Stderr, "\x1b[31m%s\x1b[0m\n", msg)
	} else {
		fmt.Fprintln(os.Stderr, msg)
	}
}

// Info prints msg to stdout in cyan, prefixed with an arrow.
func Info(msg string) {
	fmt.Println(WithColor(ColorCyan, "→ "+msg))
}

// Infof formats and prints an informational line. See Info.
func Infof(format string, args ...any) {
	Info(fmt.Sprintf(format, args...))
}

// Success prints msg to stdout in green, prefixed with a checkmark.
func Success(msg string) {
	fmt.Println(WithColor(ColorGreen, "✓ "+msg))
}

// Successf formats and prints a success line. See Success.
func Successf(format string, args ...any) {
	Success(fmt.Sprintf(format, args...))
}

// Warning prints msg to stdout in yellow, prefixed with a warning symbol.
func Warning(msg string) {
	fmt.Println(WithColor(ColorYellow, "⚠ "+msg))
}

// Warningf formats and prints a warning line. See Warning.
func Warningf(format string, args ...any) {
	Warning(fmt.Sprintf(format, args...))
}

// Error prints msg to stderr in red, prefixed with an X.
func Error(msg string) {
	fmt.Fprintln(os.Stderr, WithColor(ColorRed, "✗ "+msg))
}

// Errorf formats and prints an error line. See Error.
func Errorf(format string, args ...any) {
	Error(fmt.Sprintf(format, args...))
}

// ReadPrompt prints "label [defaultVal]: " to stderr and reads a line from the terminal.
// Returns defaultVal if the user presses enter without typing anything.
func ReadPrompt(label, defaultVal string) (string, error) {
	if !ensureTTY() {
		return "", fmt.Errorf("console.ReadPrompt requires a TTY")
	}
	if defaultVal != "" {
		fmt.Fprintf(os.Stderr, "%s [%s]: ", label, defaultVal)
	} else {
		fmt.Fprintf(os.Stderr, "%s: ", label)
	}
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		val := strings.TrimSpace(scanner.Text())
		if val == "" {
			return defaultVal, nil
		}
		return val, nil
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("reading input: %w", err)
	}
	return defaultVal, nil
}

// PromptYesNo prints question to stderr (appending " [y/N] ") and returns true
// if the user answers y or yes. Returns false if not a TTY.
func PromptYesNo(question string) bool {
	if !ensureTTY() {
		return false
	}
	fmt.Fprintf(os.Stderr, "%s [y/N] ", question)
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		ans := strings.TrimSpace(strings.ToLower(scanner.Text()))
		return ans == "y" || ans == "yes"
	}
	return false
}

// OpenBrowser attempts to open url in the default system browser.
// Returns an error if the browser cannot be launched; callers should fall back
// to printing the URL for the user to open manually.
func OpenBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		// Avoid "cmd /c start <url>": cmd.exe re-parses its own command line and
		// treats an unescaped "&" as a command separator, silently truncating any
		// URL with multiple query params (e.g. dropping redirect_uri from the SSO
		// auth URL). rundll32 receives the URL as a single argument with no shell
		// re-parsing in between.
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
