// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package vibe

import (
	"fmt"
	"net/url"

	"github.com/harness/cli/pkg/cmdctx"
)

func vibeDeploymentGetHandler(ctx *cmdctx.Ctx) error {
	appID := ctx.Id
	if appID == "" {
		return fmt.Errorf("get vibe_deployment requires <appId>")
	}
	api := newAPI(ctx)
	path := deploymentPath(appID, cmdctx.GetString(ctx.FlagValues, "execution"))
	var dep map[string]any
	if err := api.getJSON(path, &dep); err != nil {
		return err
	}
	return writeGet(ctx, dep)
}

func vibeDeploymentRetryHandler(ctx *cmdctx.Ctx) error {
	return runDeploymentAction(ctx, "retry")
}

func vibeDeploymentCancelHandler(ctx *cmdctx.Ctx) error {
	return runDeploymentAction(ctx, "cancel")
}

func vibeDeploymentPublishHandler(ctx *cmdctx.Ctx) error {
	appID := ctx.Id
	if appID == "" {
		return fmt.Errorf("execute vibe_deployment:publish requires <appId>")
	}
	api := newAPI(ctx)
	body, err := publishBody(api, appID)
	if err != nil {
		return err
	}
	var result map[string]any
	if err := api.postJSON("/api/apps/"+appID+"/publish", body, &result); err != nil {
		return err
	}
	return writeGet(ctx, result)
}

func runDeploymentAction(ctx *cmdctx.Ctx, action string) error {
	appID := ctx.Id
	if appID == "" {
		return fmt.Errorf("execute vibe_deployment:%s requires <appId>", action)
	}
	api := newAPI(ctx)
	execID, err := resolveExecutionID(api, appID, cmdctx.GetString(ctx.FlagValues, "execution"))
	if err != nil {
		return err
	}
	path := fmt.Sprintf("/api/apps/%s/executions/%s/%s", appID, execID, action)
	var dep map[string]any
	if err := api.postJSON(path, map[string]any{}, &dep); err != nil {
		return err
	}
	return writeGet(ctx, dep)
}

func deploymentPath(appID, executionID string) string {
	path := "/api/apps/" + appID + "/vibe-deployment"
	if executionID == "" {
		return path
	}
	return path + "?" + url.Values{"executionId": {executionID}}.Encode()
}

func resolveExecutionID(api *vibeAPI, appID, flagExec string) (string, error) {
	if flagExec != "" {
		return flagExec, nil
	}
	var dep map[string]any
	if err := api.getJSON(deploymentPath(appID, ""), &dep); err != nil {
		return "", err
	}
	if id, ok := dep["executionId"].(string); ok && id != "" {
		return id, nil
	}
	return "", fmt.Errorf("execution id not found; pass --execution <id>")
}

func publishBody(api *vibeAPI, appID string) (map[string]any, error) {
	changes, err := api.listJSON("/api/apps/" + appID + "/changes")
	if err != nil {
		return map[string]any{}, nil
	}
	if len(changes) == 0 {
		return map[string]any{}, nil
	}
	if id, ok := changes[0]["id"].(string); ok && id != "" {
		return map[string]any{"changeId": id}, nil
	}
	return map[string]any{}, nil
}
