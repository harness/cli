// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/harness/cli/pkg/spec"
)

func verbGerund(verb string) string {
	g := verb + "ing"
	if vs, ok := verbRegistry[verb]; ok && vs.Gerund != "" {
		g = vs.Gerund
	}
	return strings.ToUpper(g[:1]) + g[1:]
}

// runConfirmGate enforces the confirmation prompt for delete commands.
// It returns an error if the user declines or input cannot be read.
// It is a no-op when mode is ConfirmNone or force is true.
func runConfirmGate(mode string, verb string, noun string, id string, isTTY bool, force bool) error {
	if mode == spec.ConfirmNone {
		return nil
	}
	if force {
		return nil
	}
	if !isTTY {
		return fmt.Errorf("Request canceled: stdin is not a terminal; re-run with --force to skip this prompt")
	}

	gerund := verbGerund(verb)

	switch mode {
	case spec.ConfirmPrompt:
		fmt.Fprintf(os.Stderr, "%s %q — are you sure? [y/N]: ", gerund, id)
		line, err := readLine()
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return fmt.Errorf("Reading confirmation: %w", err)
		}
		if !strings.EqualFold(strings.TrimSpace(line), "y") {
			return fmt.Errorf("Request canceled")
		}

	case spec.ConfirmID:
		fmt.Fprintf(os.Stderr, "%s %s %q\nWARNING: this action is not recoverable.\nType %q to confirm: ", gerund, noun, id, id)
		line, err := readLine()
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return fmt.Errorf("Reading confirmation: %w", err)
		}
		if strings.TrimSpace(line) != id {
			return fmt.Errorf("Identifier did not match — request canceled")
		}
	}

	return nil
}

func readLine() (string, error) {
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		return scanner.Text(), nil
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("no input")
}
