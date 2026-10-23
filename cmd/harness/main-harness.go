// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"github.com/harness/cli/v3/cmd/harness/harnessapp"
	"github.com/harness/cli/v3/modules/aievals"
	"github.com/harness/cli/v3/pkg/hbase"
	"github.com/harness/cli/v3/pkg/registry"
	"github.com/harness/cli/v3/pkg/spec"
)

func main() {
	data, err := spec.Read("aievals.spec.yaml")
	if err != nil {
		panic(err)
	}
	harnessapp.Run(harnessapp.Options{
		Embedded: []harnessapp.EmbeddedModule{{
			Name:    "aievals",
			Version: hbase.Version,
			Spec:    data,
			Init: func(module registry.ModuleRegistrar) {
				aievals.ModuleInit(module)
			},
		}},
	})
}
