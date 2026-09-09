// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

// Command testexpr evaluates an expr-lang expression against a JSON environment
// and prints the result. Meant for local iteration on spec expressions, not for
// end users.
//
// Usage:
//
//	go run ./cmd/testexpr <expression> [envJSON|@file|-]
//
// The env arg defaults to "{}" when omitted. "-" reads JSON from stdin;
// "@file" reads it from a file; anything else is parsed as a literal JSON
// string. The expression is evaluated with the same helper functions
// (truncate, coalesce, epochMs, ...) available to spec expressions, injected
// via exprenv.BaseFuncs.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"strings"

	"github.com/expr-lang/expr"

	"github.com/harness/cli/v3/pkg/exprenv"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: testexpr <expression> [envJSON|@file|-]")
		os.Exit(1)
	}
	exprStr := os.Args[1]

	envArg := "{}"
	if len(os.Args) > 2 {
		envArg = os.Args[2]
	}

	envJSON, err := readEnvArg(envArg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	var env map[string]any
	if err := json.Unmarshal([]byte(envJSON), &env); err != nil {
		fmt.Fprintf(os.Stderr, "error: invalid env JSON: %v\n", err)
		os.Exit(1)
	}
	if env == nil {
		env = map[string]any{}
	}
	maps.Copy(env, exprenv.BaseFuncs(false))

	program, err := expr.Compile(exprStr, expr.Env(env), expr.AsAny())
	if err != nil {
		fmt.Fprintf(os.Stderr, "compile error: %v\n", err)
		os.Exit(1)
	}

	out, err := expr.Run(program, env)
	if err != nil {
		fmt.Fprintf(os.Stderr, "eval error: %v\n", err)
		os.Exit(1)
	}

	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		fmt.Printf("%v\n", out)
		return
	}
	fmt.Println(string(b))
}

// readEnvArg resolves the env argument into raw JSON text: "-" reads stdin,
// "@file" reads the named file, anything else is returned as-is.
func readEnvArg(arg string) (string, error) {
	switch {
	case arg == "-":
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("reading stdin: %w", err)
		}
		return string(b), nil
	case strings.HasPrefix(arg, "@"):
		b, err := os.ReadFile(arg[1:])
		if err != nil {
			return "", fmt.Errorf("reading %s: %w", arg[1:], err)
		}
		return string(b), nil
	default:
		return arg, nil
	}
}
