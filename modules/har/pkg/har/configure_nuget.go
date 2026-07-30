// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"

	"github.com/harness/cli/pkg/cmdctx"
)

func configureNuget(ctx *cmdctx.Ctx) error {
	a := ctx.Auth
	registryID := ctx.Id

	registryURL := fmt.Sprintf("%s/pkg/%s/%s/nuget/v3/index.json", a.RegistryURL, a.AccountID, registryID)

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home directory: %w", err)
	}

	nugetDir := filepath.Join(home, ".nuget", "NuGet")
	if err := os.MkdirAll(nugetDir, 0755); err != nil {
		return fmt.Errorf("creating NuGet config directory: %w", err)
	}
	configPath := filepath.Join(nugetDir, "NuGet.Config")

	if err := writeNugetConfig(configPath, registryID, registryURL, a.PATToken); err != nil {
		return fmt.Errorf("writing NuGet.Config: %w", err)
	}

	_ = savePkgmgrConfig("nuget", pkgmgrSavedConfig{
		RegistryIdentifier: registryID,
		RegistryURL:        registryURL,
		OrgID:              a.OrgID,
		ProjectID:          a.ProjectID,
	})

	fmt.Printf("Configured NuGet → %s (%s)\n", registryURL, configPath)
	return nil
}

func writeNugetConfig(configPath, registryID, registryURL, authToken string) error {
	type Add struct {
		XMLName         xml.Name `xml:"add"`
		Key             string   `xml:"key,attr"`
		Value           string   `xml:"value,attr"`
		ProtocolVersion *string  `xml:"protocolVersion,attr,omitempty"`
	}
	type PackageSources struct {
		XMLName xml.Name `xml:"packageSources"`
		Sources []Add    `xml:"add"`
	}
	type CredentialAdd struct {
		XMLName xml.Name `xml:"add"`
		Key     string   `xml:"key,attr"`
		Value   string   `xml:"value,attr"`
	}
	type SourceCredential struct {
		XMLName xml.Name
		Adds    []CredentialAdd `xml:"add"`
	}
	type PackageSourceCredentials struct {
		XMLName xml.Name           `xml:"packageSourceCredentials"`
		Sources []SourceCredential `xml:",any"`
	}
	type Configuration struct {
		XMLName                  xml.Name                  `xml:"configuration"`
		PackageSources           PackageSources            `xml:"packageSources"`
		PackageSourceCredentials *PackageSourceCredentials `xml:"packageSourceCredentials,omitempty"`
	}

	conf := Configuration{}

	if data, err := os.ReadFile(configPath); err == nil && len(data) > 0 {
		if xmlErr := xml.Unmarshal(data, &conf); xmlErr != nil {
			conf = Configuration{}
		}
	}

	sourceName := "harness-" + registryID
	protocolVersion := "3"

	sourceFound := false
	for i, s := range conf.PackageSources.Sources {
		if s.Key == sourceName {
			conf.PackageSources.Sources[i].Value = registryURL
			conf.PackageSources.Sources[i].ProtocolVersion = &protocolVersion
			sourceFound = true
			break
		}
	}
	if !sourceFound {
		conf.PackageSources.Sources = append(conf.PackageSources.Sources, Add{
			Key:             sourceName,
			Value:           registryURL,
			ProtocolVersion: &protocolVersion,
		})
	}

	if conf.PackageSourceCredentials == nil {
		conf.PackageSourceCredentials = &PackageSourceCredentials{}
	}
	credFound := false
	for i, src := range conf.PackageSourceCredentials.Sources {
		if src.XMLName.Local == sourceName {
			conf.PackageSourceCredentials.Sources[i].Adds = []CredentialAdd{
				{Key: "Username", Value: "harness"},
				{Key: "ClearTextPassword", Value: authToken},
			}
			credFound = true
			break
		}
	}
	if !credFound {
		conf.PackageSourceCredentials.Sources = append(conf.PackageSourceCredentials.Sources, SourceCredential{
			XMLName: xml.Name{Local: sourceName},
			Adds: []CredentialAdd{
				{Key: "Username", Value: "harness"},
				{Key: "ClearTextPassword", Value: authToken},
			},
		})
	}

	out, err := xml.MarshalIndent(conf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling NuGet.Config: %w", err)
	}

	return atomicWrite(configPath, []byte(xml.Header+string(out)+"\n"), 0600)
}
