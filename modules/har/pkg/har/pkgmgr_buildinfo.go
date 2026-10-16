// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/harness/cli/modules/har/pkg/har/genapi/ar_v3"

	"github.com/harness/cli/v3/pkg/auth"
)

type buildInfoUploader interface {
	AddBuildInfoWithResponse(
		context.Context,
		*ar_v3.AddBuildInfoParams,
		ar_v3.AddBuildInfoJSONRequestBody,
		...ar_v3.RequestEditorFn,
	) (*ar_v3.AddBuildInfoResp, error)
}

type rootPackageInfo struct {
	Name    string
	Version string
}

// pkgmgrBuildInfoClient returns the client and account used for AddBuildInfo.
// BUILD_INFO_URL replaces the default HAR v3 base URL; BUILD_INFO_ACCOUNT
// replaces the account query param when that override is set.
func pkgmgrBuildInfoClient(hc *http.Client, a *auth.ResolvedAuth, fallback buildInfoUploader) (buildInfoUploader, string) {
	accountID := a.AccountID
	overrideURL := os.Getenv("BUILD_INFO_URL")
	if overrideURL == "" {
		return fallback, accountID
	}
	if overrideAccount := os.Getenv("BUILD_INFO_ACCOUNT"); overrideAccount != "" {
		accountID = overrideAccount
	}
	client, err := ar_v3.NewClientWithResponses(overrideURL, ar_v3.WithHTTPClient(hc), ar_v3.WithRequestEditorFn(func(_ context.Context, req *http.Request) error {
		a.SetAuthHeader(req)
		return nil
	}))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Build info URL override failed, using the default client: %v\n", err)
		return fallback, a.AccountID
	}
	fmt.Fprintf(os.Stderr, "Using BUILD_INFO_URL for build info upload\n")
	return client, accountID
}

// pkgmgrUploadBuildInfo records the blocked install's dependency graph on the
// registry. A missing pipeline context or a failed upload leaves the firewall
// failure as the command result.
func pkgmgrUploadBuildInfo(ctx context.Context, client buildInfoUploader, accountID, packageType string, registryUUID uuid.UUID, deps []dependency) {
	pipeline := pkgmgrPipelineContext()
	if pipeline == nil {
		fmt.Fprintf(os.Stderr, "Skipping build info upload: required HARNESS_* pipeline env vars are missing\n")
		return
	}

	fmt.Fprintf(os.Stderr, "Uploading build info...\n")
	body := pkgmgrBuildInfoRequest(packageType, registryUUID, deps, pipeline)
	resp, err := client.AddBuildInfoWithResponse(ctx, &ar_v3.AddBuildInfoParams{
		AccountIdentifier: accountID,
	}, body)
	if err != nil || resp == nil {
		fmt.Fprintf(os.Stderr, "Build info upload failed (non-fatal): %v\n", err)
		return
	}
	if resp.StatusCode() == http.StatusCreated {
		fmt.Fprintf(os.Stderr, "Build info uploaded (%d nodes)\n", len(body.Metadata))
		return
	}
	fmt.Fprintf(os.Stderr, "Build info upload returned status %d (non-fatal)\n", resp.StatusCode())
}

func pkgmgrBuildInfoRequest(packageType string, registryUUID uuid.UUID, deps []dependency, pipeline *ar_v3.PipelineContext) ar_v3.BuildInfoRequestInput {
	root := pkgmgrDetectRootPackage(packageType)
	rootKey := root.Name + "@" + root.Version
	nodes := make([]ar_v3.BuildInfoNode, 0, len(deps))
	for _, dep := range deps {
		parent := rootKey
		if dep.Parent != "" {
			parent = dep.Parent
		}
		nodes = append(nodes, ar_v3.BuildInfoNode{
			NodeKey:       dep.Name + "@" + dep.Version,
			ParentNodeKey: &parent,
		})
	}
	return ar_v3.BuildInfoRequestInput{
		RegistryId:      registryUUID,
		PackageType:     packageType,
		RootPackage:     ar_v3.RootPackage{Name: root.Name, Version: root.Version},
		Status:          ar_v3.BuildInfoRequestInputStatusFAILURE,
		Metadata:        nodes,
		PipelineContext: pipeline,
	}
}

func pkgmgrPipelineContext() *ar_v3.PipelineContext {
	pipelineID := os.Getenv("HARNESS_PIPELINE_ID")
	executionID := os.Getenv("HARNESS_EXECUTION_ID")
	orgID := os.Getenv("HARNESS_ORG_ID")
	projectID := os.Getenv("HARNESS_PROJECT_ID")
	if pipelineID == "" || executionID == "" || orgID == "" || projectID == "" {
		return nil
	}
	stageID := os.Getenv("HARNESS_STAGE_ID")
	if stageID == "" {
		stageID = "default"
	}
	ctx := &ar_v3.PipelineContext{
		PipelineId:  pipelineID,
		ExecutionId: executionID,
		OrgId:       orgID,
		ProjectId:   projectID,
		StageId:     stageID,
	}
	if stepID := os.Getenv("HARNESS_STEP_ID"); stepID != "" {
		ctx.StepId = &stepID
	}
	return ctx
}

func pkgmgrDetectRootPackage(packageType string) rootPackageInfo {
	switch packageType {
	case "NPM":
		return detectNpmRootPackage()
	case "MAVEN":
		return detectMavenRootPackage()
	case "PYTHON":
		return detectPythonRootPackage()
	case "NUGET":
		return detectNugetRootPackage()
	default:
		return rootPackageInfo{Name: "unknown", Version: "0.0.0"}
	}
}

func detectNpmRootPackage() rootPackageInfo {
	data, err := os.ReadFile("package.json")
	if err != nil {
		return rootPackageInfo{Name: "unknown", Version: "0.0.0"}
	}
	var pkg struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil || pkg.Name == "" {
		return rootPackageInfo{Name: "unknown", Version: "0.0.0"}
	}
	if pkg.Version == "" {
		pkg.Version = "0.0.0"
	}
	return rootPackageInfo{Name: pkg.Name, Version: pkg.Version}
}

func detectMavenRootPackage() rootPackageInfo {
	data, err := os.ReadFile("pom.xml")
	if err != nil {
		return rootPackageInfo{Name: "unknown", Version: "0.0.0"}
	}
	var pom struct {
		XMLName    xml.Name `xml:"project"`
		GroupID    string   `xml:"groupId"`
		ArtifactID string   `xml:"artifactId"`
		Version    string   `xml:"version"`
		Parent     struct {
			GroupID string `xml:"groupId"`
			Version string `xml:"version"`
		} `xml:"parent"`
	}
	if err := xml.Unmarshal(data, &pom); err != nil {
		return rootPackageInfo{Name: "unknown", Version: "0.0.0"}
	}
	groupID := pom.GroupID
	if groupID == "" {
		groupID = pom.Parent.GroupID
	}
	version := pom.Version
	if version == "" {
		version = pom.Parent.Version
	}
	if groupID == "" || pom.ArtifactID == "" {
		return rootPackageInfo{Name: "unknown", Version: "0.0.0"}
	}
	if version == "" {
		version = "0.0.0"
	}
	return rootPackageInfo{Name: groupID + ":" + pom.ArtifactID, Version: version}
}

func detectPythonRootPackage() rootPackageInfo {
	if data, err := os.ReadFile("pyproject.toml"); err == nil {
		name, version := pythonProjectFields(string(data))
		if name != "" {
			if version == "" {
				version = "0.0.0"
			}
			return rootPackageInfo{Name: name, Version: version}
		}
	}
	if data, err := os.ReadFile("setup.cfg"); err == nil {
		name, version := pythonProjectFields(string(data))
		if name != "" {
			if version == "" {
				version = "0.0.0"
			}
			return rootPackageInfo{Name: name, Version: version}
		}
	}
	return rootPackageInfo{Name: "unknown", Version: "0.0.0"}
}

func pythonProjectFields(content string) (string, string) {
	var name, version string
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), "\"'")
		switch key {
		case "name":
			name = value
		case "version":
			version = value
		}
	}
	return name, version
}

func detectNugetRootPackage() rootPackageInfo {
	csprojFiles, _ := filepath.Glob("*.csproj")
	if len(csprojFiles) == 0 {
		return rootPackageInfo{Name: "unknown", Version: "0.0.0"}
	}
	name := strings.TrimSuffix(filepath.Base(csprojFiles[0]), ".csproj")
	version := "0.0.0"
	data, err := os.ReadFile(csprojFiles[0])
	if err != nil {
		return rootPackageInfo{Name: name, Version: version}
	}
	var proj struct {
		PropertyGroups []struct {
			AssemblyName string `xml:"AssemblyName"`
			Version      string `xml:"Version"`
			PackageID    string `xml:"PackageId"`
		} `xml:"PropertyGroup"`
	}
	if err := xml.Unmarshal(data, &proj); err != nil {
		return rootPackageInfo{Name: name, Version: version}
	}
	for _, pg := range proj.PropertyGroups {
		if pg.PackageID != "" {
			name = pg.PackageID
		} else if pg.AssemblyName != "" && name == strings.TrimSuffix(filepath.Base(csprojFiles[0]), ".csproj") {
			name = pg.AssemblyName
		}
		if pg.Version != "" {
			version = pg.Version
		}
	}
	return rootPackageInfo{Name: name, Version: version}
}
