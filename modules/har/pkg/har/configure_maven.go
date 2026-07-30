// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/harness/cli/pkg/cmdctx"
)

func configureMaven(ctx *cmdctx.Ctx) error {
	a := ctx.Auth
	registryID := ctx.Id

	registryURL := fmt.Sprintf("%s/pkg/%s/%s/maven", a.RegistryURL, a.AccountID, registryID)

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home directory: %w", err)
	}

	m2Dir := filepath.Join(home, ".m2")
	if err := os.MkdirAll(m2Dir, 0755); err != nil {
		return fmt.Errorf("creating .m2 directory: %w", err)
	}
	settingsPath := filepath.Join(m2Dir, "settings.xml")

	if err := writeMavenSettings(settingsPath, registryID, registryURL, a.PATToken); err != nil {
		return fmt.Errorf("writing settings.xml: %w", err)
	}

	_ = savePkgmgrConfig("maven", pkgmgrSavedConfig{
		RegistryIdentifier: registryID,
		RegistryURL:        registryURL,
		OrgID:              a.OrgID,
		ProjectID:          a.ProjectID,
	})

	fmt.Printf("Configured Maven → %s (%s)\n", registryURL, settingsPath)
	return nil
}

func writeMavenSettings(settingsPath, registryID, registryURL, authToken string) error {
	type Server struct {
		XMLName  xml.Name `xml:"server"`
		ID       string   `xml:"id"`
		Username string   `xml:"username"`
		Password string   `xml:"password"`
	}
	type Mirror struct {
		XMLName  xml.Name `xml:"mirror"`
		ID       string   `xml:"id"`
		Name     string   `xml:"name"`
		URL      string   `xml:"url"`
		MirrorOf string   `xml:"mirrorOf"`
	}
	type RepoEnabled struct {
		Enabled bool `xml:"enabled"`
	}
	type Repository struct {
		ID        string      `xml:"id"`
		URL       string      `xml:"url"`
		Releases  RepoEnabled `xml:"releases"`
		Snapshots RepoEnabled `xml:"snapshots"`
	}
	type PluginRepository struct {
		ID        string      `xml:"id"`
		URL       string      `xml:"url"`
		Releases  RepoEnabled `xml:"releases"`
		Snapshots RepoEnabled `xml:"snapshots"`
	}
	type Profile struct {
		XMLName      xml.Name `xml:"profile"`
		ID           string   `xml:"id"`
		Repositories struct {
			XMLName    xml.Name     `xml:"repositories"`
			Repository []Repository `xml:"repository"`
		}
		PluginRepositories struct {
			XMLName          xml.Name           `xml:"pluginRepositories"`
			PluginRepository []PluginRepository `xml:"pluginRepository"`
		}
	}
	type Settings struct {
		XMLName xml.Name `xml:"settings"`
		Xmlns   string   `xml:"xmlns,attr"`
		Servers struct {
			XMLName xml.Name `xml:"servers"`
			Server  []Server `xml:"server"`
		}
		Mirrors struct {
			XMLName xml.Name `xml:"mirrors"`
			Mirror  []Mirror `xml:"mirror"`
		}
		Profiles struct {
			XMLName xml.Name  `xml:"profiles"`
			Profile []Profile `xml:"profile"`
		}
		ActiveProfiles struct {
			XMLName       xml.Name `xml:"activeProfiles"`
			ActiveProfile []string `xml:"activeProfile"`
		}
	}

	settings := Settings{Xmlns: "http://maven.apache.org/SETTINGS/1.0.0"}

	if data, err := os.ReadFile(settingsPath); err == nil && len(data) > 0 {
		if xmlErr := xml.Unmarshal(data, &settings); xmlErr != nil {
			settings = Settings{Xmlns: "http://maven.apache.org/SETTINGS/1.0.0"}
		}
	}

	serverID := "harness-" + registryID

	// Strip any Harness-managed mirrors entirely — mirrorOf: "*" breaks
	// resolution of Maven's own core lifecycle plugins, so previously
	// written broken configs self-heal here with no replacement.
	var mirrors []Mirror
	for _, m := range settings.Mirrors.Mirror {
		if strings.HasPrefix(m.ID, "harness-") {
			continue
		}
		mirrors = append(mirrors, m)
	}
	settings.Mirrors.Mirror = mirrors

	var profiles []Profile
	for _, p := range settings.Profiles.Profile {
		if strings.HasPrefix(p.ID, "harness-") {
			continue
		}
		profiles = append(profiles, p)
	}
	newProfile := Profile{ID: serverID}
	newProfile.Repositories.Repository = []Repository{{
		ID:        serverID,
		URL:       registryURL,
		Releases:  RepoEnabled{Enabled: true},
		Snapshots: RepoEnabled{Enabled: true},
	}}
	newProfile.PluginRepositories.PluginRepository = []PluginRepository{{
		ID:        serverID,
		URL:       registryURL,
		Releases:  RepoEnabled{Enabled: true},
		Snapshots: RepoEnabled{Enabled: true},
	}}
	profiles = append(profiles, newProfile)
	settings.Profiles.Profile = profiles

	var activeProfiles []string
	for _, ap := range settings.ActiveProfiles.ActiveProfile {
		if strings.HasPrefix(ap, "harness-") {
			continue
		}
		activeProfiles = append(activeProfiles, ap)
	}
	activeProfiles = append(activeProfiles, serverID)
	settings.ActiveProfiles.ActiveProfile = activeProfiles

	var servers []Server
	for _, s := range settings.Servers.Server {
		if s.ID == serverID || strings.HasPrefix(s.ID, "harness-") {
			continue
		}
		servers = append(servers, s)
	}
	servers = append(servers, Server{
		ID:       serverID,
		Username: "harness",
		Password: authToken,
	})
	settings.Servers.Server = servers

	out, err := xml.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling settings.xml: %w", err)
	}

	return atomicWrite(settingsPath, []byte(xml.Header+string(out)+"\n"), 0600)
}
