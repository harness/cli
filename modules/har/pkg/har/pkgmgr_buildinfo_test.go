// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"context"
	"net/http"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/harness/cli/modules/har/pkg/har/genapi/ar_v3"
)

type fakeBuildInfoClient struct {
	resp   *ar_v3.AddBuildInfoResp
	err    error
	calls  int
	params *ar_v3.AddBuildInfoParams
	body   ar_v3.AddBuildInfoJSONRequestBody
}

func (f *fakeBuildInfoClient) AddBuildInfoWithResponse(
	_ context.Context,
	params *ar_v3.AddBuildInfoParams,
	body ar_v3.AddBuildInfoJSONRequestBody,
	_ ...ar_v3.RequestEditorFn,
) (*ar_v3.AddBuildInfoResp, error) {
	f.calls++
	f.params = params
	f.body = body
	return f.resp, f.err
}

func TestPkgmgrUploadBuildInfoSkipsWithoutPipelineEnv(t *testing.T) {
	clearPipelineEnv(t)
	fake := &fakeBuildInfoClient{}
	pkgmgrUploadBuildInfo(context.Background(), fake, "acct", "NPM", uuid.New(), []dependency{{Name: "left-pad", Version: "1.3.0"}})
	require.Zero(t, fake.calls)
}

func TestPkgmgrUploadBuildInfoPostsFailureGraph(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.WriteFile("package.json", []byte(`{"name":"my-app","version":"1.2.3"}`), 0o644))
	setPipelineEnv(t)
	t.Setenv("BUILD_INFO_URL", "")
	t.Setenv("BUILD_INFO_ACCOUNT", "")

	reg := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	fake := &fakeBuildInfoClient{resp: &ar_v3.AddBuildInfoResp{
		HTTPResponse: &http.Response{StatusCode: http.StatusCreated},
	}}
	pkgmgrUploadBuildInfo(context.Background(), fake, "acct", "NPM", reg, []dependency{
		{Name: "left-pad", Version: "1.3.0"},
		{Name: "ms", Version: "2.1.3", Parent: "left-pad@1.3.0"},
	})

	require.Equal(t, 1, fake.calls)
	require.Equal(t, "acct", fake.params.AccountIdentifier)
	require.Equal(t, reg, fake.body.RegistryId)
	require.Equal(t, "NPM", fake.body.PackageType)
	require.Equal(t, ar_v3.BuildInfoRequestInputStatusFAILURE, fake.body.Status)
	require.Equal(t, "my-app", fake.body.RootPackage.Name)
	require.Equal(t, "1.2.3", fake.body.RootPackage.Version)
	require.Equal(t, "pipe", fake.body.PipelineContext.PipelineId)
	require.Equal(t, "exec", fake.body.PipelineContext.ExecutionId)
	require.Equal(t, "build", fake.body.PipelineContext.StageId)
	require.NotNil(t, fake.body.PipelineContext.StepId)
	require.Equal(t, "install", *fake.body.PipelineContext.StepId)
	require.Equal(t, "left-pad@1.3.0", fake.body.Metadata[0].NodeKey)
	require.Equal(t, "my-app@1.2.3", *fake.body.Metadata[0].ParentNodeKey)
	require.Equal(t, "ms@2.1.3", fake.body.Metadata[1].NodeKey)
	require.Equal(t, "left-pad@1.3.0", *fake.body.Metadata[1].ParentNodeKey)
}

func TestPkgmgrUploadBuildInfoKeepsFirewallFailureOnAPIError(t *testing.T) {
	setPipelineEnv(t)
	fake := &fakeBuildInfoClient{resp: &ar_v3.AddBuildInfoResp{
		HTTPResponse: &http.Response{StatusCode: http.StatusInternalServerError},
	}}
	pkgmgrUploadBuildInfo(context.Background(), fake, "acct", "NPM", uuid.New(), nil)
	require.Equal(t, 1, fake.calls)
	require.Equal(t, ar_v3.BuildInfoRequestInputStatusFAILURE, fake.body.Status)
}

func TestPkgmgrPipelineContextDefaultsStage(t *testing.T) {
	t.Setenv("HARNESS_PIPELINE_ID", "pipe")
	t.Setenv("HARNESS_EXECUTION_ID", "exec")
	t.Setenv("HARNESS_ORG_ID", "org")
	t.Setenv("HARNESS_PROJECT_ID", "proj")
	t.Setenv("HARNESS_STAGE_ID", "")
	t.Setenv("HARNESS_STEP_ID", "")

	got := pkgmgrPipelineContext()
	require.NotNil(t, got)
	require.Equal(t, "default", got.StageId)
	require.Nil(t, got.StepId)
}

func TestPkgmgrDetectRootPackage(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	require.Equal(t, rootPackageInfo{Name: "unknown", Version: "0.0.0"}, pkgmgrDetectRootPackage("NPM"))

	require.NoError(t, os.WriteFile("package.json", []byte(`{"name":"my-app"}`), 0o644))
	require.Equal(t, rootPackageInfo{Name: "my-app", Version: "0.0.0"}, detectNpmRootPackage())

	require.NoError(t, os.WriteFile("pom.xml", []byte(`
<project>
  <parent><groupId>io.example</groupId><version>9.0.0</version></parent>
  <artifactId>lib</artifactId>
</project>`), 0o644))
	require.Equal(t, rootPackageInfo{Name: "io.example:lib", Version: "9.0.0"}, detectMavenRootPackage())

	require.NoError(t, os.WriteFile("pyproject.toml", []byte("name = \"py-app\"\nversion = \"2.0.0\"\n"), 0o644))
	require.Equal(t, rootPackageInfo{Name: "py-app", Version: "2.0.0"}, detectPythonRootPackage())

	require.NoError(t, os.WriteFile("App.csproj", []byte(`
<Project>
  <PropertyGroup>
    <PackageId>Contoso.App</PackageId>
    <Version>3.1.0</Version>
  </PropertyGroup>
</Project>`), 0o644))
	require.Equal(t, rootPackageInfo{Name: "Contoso.App", Version: "3.1.0"}, detectNugetRootPackage())
}

func TestMvnParseDependencyTreeParents(t *testing.T) {
	t.Parallel()
	output := `[INFO] io.harness.test:mvn-install-test:jar:1.0.0
[INFO] +- com.google.guava:guava:jar:31.1-jre:compile
[INFO] |  +- com.google.guava:failureaccess:jar:1.0.1:compile
[INFO] |  \- com.google.j2objc:j2objc-annotations:jar:1.3:compile
[INFO] \- org.apache.commons:commons-lang3:jar:3.12.0:compile
`
	deps := mvnParseDependencyTree(output)
	require.Len(t, deps, 4)
	require.Equal(t, dependency{Name: "com.google.guava:guava", Version: "31.1-jre"}, deps[0])
	require.Equal(t, dependency{
		Name: "com.google.guava:failureaccess", Version: "1.0.1", Parent: "com.google.guava:guava@31.1-jre",
	}, deps[1])
	require.Equal(t, dependency{
		Name: "com.google.j2objc:j2objc-annotations", Version: "1.3", Parent: "com.google.guava:guava@31.1-jre",
	}, deps[2])
	require.Equal(t, dependency{Name: "org.apache.commons:commons-lang3", Version: "3.12.0"}, deps[3])
}

func setPipelineEnv(t *testing.T) {
	t.Helper()
	t.Setenv("HARNESS_PIPELINE_ID", "pipe")
	t.Setenv("HARNESS_EXECUTION_ID", "exec")
	t.Setenv("HARNESS_ORG_ID", "org")
	t.Setenv("HARNESS_PROJECT_ID", "proj")
	t.Setenv("HARNESS_STAGE_ID", "build")
	t.Setenv("HARNESS_STEP_ID", "install")
}

func clearPipelineEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"HARNESS_PIPELINE_ID",
		"HARNESS_EXECUTION_ID",
		"HARNESS_ORG_ID",
		"HARNESS_PROJECT_ID",
		"HARNESS_STAGE_ID",
		"HARNESS_STEP_ID",
	} {
		t.Setenv(key, "")
	}
}
