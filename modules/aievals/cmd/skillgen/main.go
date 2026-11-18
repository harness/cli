// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

// Command skillgen copies the canonical AI Evals skill into a TypeScript module.
// It has no dependencies beyond the standard library and this module.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/harness/cli/v3/modules/aievals"
	"github.com/harness/cli/v3/pkg/hbase"
)

func main() {
	hbase.Exit(run())
}

func run() int {
	skillPath := flag.String("skill", aievals.SkillSourcePath, "canonical skill file to read")
	sourceLabel := flag.String("source-label", aievals.SkillSourcePath, "source path recorded in the generated module")
	outPath := flag.String("out", "", "TypeScript module to write")
	flag.Parse()
	if *outPath == "" {
		fmt.Fprintln(os.Stderr, "usage: go run ./modules/aievals/cmd/skillgen -out <file> [-skill modules/aievals/skill/SKILL.md]")
		return 2
	}
	body, err := os.ReadFile(*skillPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	rendered := aievals.FormatSkillModule(*sourceLabel, string(body), *skillPath)
	if err := os.MkdirAll(filepath.Dir(*outPath), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := os.WriteFile(*outPath, []byte(rendered), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
