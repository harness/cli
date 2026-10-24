// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/harness/cli/v3/pkg/client"
	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/format"
)

const (
	deleteArtifactHandlerID        = "delete_artifact"
	deleteArtifactVersionHandlerID = "delete_artifact_version"
)

type artifactTarget struct {
	Registry string
	Name     string
	Version  string
}

// parseArtifactTarget splits <registry>/<name>[/<version>]. The name may itself
// contain '/' (Terraform modules are named namespace/name/provider), so the
// registry is the first segment and the version is the last.
func parseArtifactTarget(id string, withVersion bool) (artifactTarget, error) {
	label := "<registry/name>"
	minParts := 2
	if withVersion {
		label = "<registry/name/version>"
		minParts = 3
	}
	parts := strings.Split(id, "/")
	if len(parts) < minParts || strings.Contains("/"+id+"/", "//") {
		return artifactTarget{}, fmt.Errorf("expected %s, got %q", label, id)
	}
	t := artifactTarget{Registry: parts[0]}
	if withVersion {
		t.Name = strings.Join(parts[1:len(parts)-1], "/")
		t.Version = parts[len(parts)-1]
	} else {
		t.Name = strings.Join(parts[1:], "/")
	}
	return t, nil
}

func (t artifactTarget) path(scope string) string {
	p := fmt.Sprintf("/har/api/v1/registry/%s/%s/+/artifact/%s/+", scope, t.Registry, t.Name)
	if t.Version != "" {
		p += "/version/" + t.Version
	}
	return p
}

func deleteArtifactHandler(ctx *cmdctx.Ctx) error {
	return runArtifactDelete(ctx, false)
}

func deleteArtifactVersionHandler(ctx *cmdctx.Ctx) error {
	return runArtifactDelete(ctx, true)
}

func runArtifactDelete(ctx *cmdctx.Ctx, withVersion bool) error {
	target, err := parseArtifactTarget(ctx.Id, withVersion)
	if err != nil {
		return err
	}
	previewOnly := ctx.RequestPreview != nil
	if !previewOnly && !cmdctx.GetBool(ctx.FlagValues, "force") {
		if !stdinIsTerminal() {
			return fmt.Errorf("confirmation required: stdin is not a terminal; re-run with --force to skip this prompt")
		}
		if err := confirmPrompt(fmt.Sprintf("Deleting %q — are you sure? [y/N]: ", ctx.Id)); err != nil {
			return err
		}
	}

	a := ctx.Auth
	scope := scopePath(a.AccountID, a.OrgID, a.ProjectID)
	path := target.path(scope)
	qp := map[string]string{"orgIdentifier": a.OrgID, "projectIdentifier": a.ProjectID}
	c := client.New(ctx)
	artifactType := cmdctx.GetString(ctx.FlagValues, "artifact-type")
	if artifactType == "" {
		if artifactType, err = lookupArtifactType(c, scope, target, qp); err != nil {
			return err
		}
	}
	qp["artifact_type"] = artifactType
	result, _, err := c.Delete(path, qp)
	if errors.Is(err, client.ErrRequestPreviewed) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := verifyArtifactGone(c, path, qp, ctx.Id); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Deleted %s\n", ctx.Id)
	return format.FormatSingleOutput(ctx.FormatFlags, ctx.IsPty, result, "", "", nil, nil, nil)
}

// lookupArtifactType finds the sub-type (e.g. Terraform "module" or "provider")
// of the named artifact. The v1 delete and summary endpoints only match typed
// artifacts when artifact_type is sent, and match untyped ones only when it isn't.
func lookupArtifactType(c *client.Client, scope string, t artifactTarget, qp map[string]string) (string, error) {
	const pageSize = 100
	listQP := map[string]string{"reg_identifier": t.Registry, "search_term": t.Name, "size": strconv.Itoa(pageSize)}
	for k, v := range qp {
		listQP[k] = v
	}
	found := map[string]bool{}
	for page := 0; ; page++ {
		listQP["page"] = strconv.Itoa(page)
		res, _, err := c.Get("/har/api/v1/spaces/"+scope+"/+/artifacts", listQP)
		if err != nil {
			return "", fmt.Errorf("looking up artifact %s/%s: %w", t.Registry, t.Name, err)
		}
		body, _ := res.(map[string]any)
		data, _ := body["data"].(map[string]any)
		rows, _ := data["artifacts"].([]any)
		for _, r := range rows {
			row, _ := r.(map[string]any)
			if row["name"] != t.Name || row["registryIdentifier"] != t.Registry {
				continue
			}
			at, _ := row["artifactType"].(string)
			found[at] = true
		}
		pageCount, _ := data["pageCount"].(float64)
		if len(rows) < pageSize || float64(page+1) >= pageCount {
			break
		}
	}
	if len(found) > 1 {
		return "", fmt.Errorf("%s/%s matches more than one artifact type; pass --artifact-type", t.Registry, t.Name)
	}
	for at := range found {
		return at, nil
	}
	return "", nil
}

// verifyArtifactGone reads the target back after a successful DELETE; only a
// 404 counts as removed.
func verifyArtifactGone(c *client.Client, path string, qp map[string]string, id string) error {
	_, _, err := c.Get(path+"/summary", qp)
	if err == nil {
		return fmt.Errorf("delete returned success but %s is still present", id)
	}
	var apiErr *client.APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
		return nil
	}
	return fmt.Errorf("delete returned success but removal of %s could not be confirmed: %w", id, err)
}

func scopePath(accountID, orgID, projectID string) string {
	scope := accountID
	if orgID != "" {
		scope += "/" + orgID
		if projectID != "" {
			scope += "/" + projectID
		}
	}
	return scope
}
