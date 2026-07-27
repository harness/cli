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
	}

	settings := Settings{Xmlns: "http://maven.apache.org/SETTINGS/1.0.0"}

	if data, err := os.ReadFile(settingsPath); err == nil && len(data) > 0 {
		if xmlErr := xml.Unmarshal(data, &settings); xmlErr != nil {
			settings = Settings{Xmlns: "http://maven.apache.org/SETTINGS/1.0.0"}
		}
	}

	serverID := "harness-" + registryID

	var mirrors []Mirror
	for _, m := range settings.Mirrors.Mirror {
		if m.ID == serverID {
			continue
		}
		if strings.HasPrefix(m.ID, "harness-") && m.MirrorOf == "*" {
			continue
		}
		mirrors = append(mirrors, m)
	}
	mirrors = append(mirrors, Mirror{
		ID:       serverID,
		Name:     "Harness " + registryID,
		URL:      registryURL,
		MirrorOf: "*",
	})
	settings.Mirrors.Mirror = mirrors

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
