// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package gitops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/harness/cli/v3/pkg/client"
	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/console"
	"github.com/harness/cli/v3/pkg/format"
	"github.com/harness/cli/v3/pkg/registry"
	"github.com/harness/cli/v3/pkg/spec"
)

const (
	installWorkflowID               = "gitops_agent_install"
	importAgentWorkflowID           = "gitops_agent_import"
	listAutocreateLogWorkflowID     = "list_gitops_autocreate_log"
	createAppProjectMappingBodyFnID = "create_app_project_mapping_body"
	deleteAppProjectMappingBodyFnID = "delete_app_project_mapping_body"
	updateAppProjectMappingBodyFnID = "update_app_project_mapping_body"
	importAgentBodyFnID             = "gitops_agent_import_body"
	importAgentSummaryFormatterID   = "gitops_agent_import_summary"
	setArgArgoProject               = "argoproject"
	setArgOrg                       = "org"
	setArgProject                   = "project"
	setArgAutoCreateServiceEnv      = "autoCreateServiceEnv"
	setArgTrue                      = "true"
	setArgFalse                     = "false"
	flagFile                        = "file"
	flagArgoProjectNames            = "argo-project-names"
	flagWatch                       = "watch"
	flagImportRequestID             = "import-request-id"
	flagOffset                      = "offset"
	flagLimit                       = "limit"
	flagAll                         = "all"
	flagCount                       = "count"
	autocreateLogWatchInterval      = 10 * time.Second
	autocreateLogWatchDefaultStop   = 2 * time.Minute
	yamlKeyMappings                 = "mappings"
	yamlKeyArgoProjectNames         = "argoProjectNames"
	wireKeyAppProjMap               = "appProjMap"
	wireKeyOrgIdentifier            = "orgIdentifier"
	wireKeyProjectIdentifier        = "projectIdentifier"
	wireKeyAutoCreateServiceEnv     = "autoCreateServiceEnv"
	wireKeyProjectNames             = "projectNames"
)

// ModuleInit registers gitops workflows and body constructors. Commands are declared in gitops.spec.yaml.
func ModuleInit(reg registry.ModuleRegistrar) {
	reg.RegisterWorkflow(installWorkflowID, executeAgentInstall)
	reg.RegisterWorkflow(importAgentWorkflowID, executeGitopsAgentImport)
	reg.RegisterWorkflow(listAutocreateLogWorkflowID, listGitopsAutocreateLog)
	reg.RegisterBodyFn(createAppProjectMappingBodyFnID, createAppProjectMappingBody)
	reg.RegisterBodyFn(deleteAppProjectMappingBodyFnID, deleteAppProjectMappingBody)
	reg.RegisterBodyFn(updateAppProjectMappingBodyFnID, updateAppProjectMappingBody)
	reg.RegisterBodyFn(importAgentBodyFnID, gitopsAgentImportBody)
	reg.RegisterTextFormatter(importAgentSummaryFormatterID, formatGitopsAgentImportSummary)
}

func executeGitopsAgentImport(ctx *cmdctx.Ctx) error {
	cs := ctx.Resolver.GetSpec("execute", "gitops_agent:import")
	if cs == nil || cs.Endpoint == nil {
		return fmt.Errorf("no spec found for execute gitops_agent:import")
	}
	result, err := ctx.Resolver.RunEndpoint(ctx, cs.Endpoint)
	if err != nil {
		return err
	}
	id, expected := importAutocreatePlan(result)
	if id == "" || expected == 0 {
		return nil
	}
	if cmdctx.GetBool(ctx.FlagValues, flagWatch) {
		return startAutocreateLogWatchAfterImport(ctx, id, expected)
	}
	if !shouldPromptAutocreateWatch(ctx) {
		return nil
	}
	if !console.PromptYesNo("Watch auto-create logs now?") {
		return nil
	}
	return startAutocreateLogWatchAfterImport(ctx, id, expected)
}

func importAutocreatePlan(result any) (importRequestID string, expected int64) {
	root, _ := result.(map[string]any)
	if root == nil {
		return "", 0
	}
	importRequestID = autocreateLogStringField(root, "importRequestId")
	recon, _ := root["reconcileAppResponse"].(map[string]any)
	counts, _ := recon["autoCreateCounts"].(map[string]any)
	if counts == nil {
		return importRequestID, 0
	}
	svc := jsonNumberAsInt64(counts["serviceCount"])
	env := jsonNumberAsInt64(counts["environmentCount"])
	links := jsonNumberAsInt64(counts["clusterLinkCount"])
	return importRequestID, svc + env + links
}

// jsonNumberAsInt64 matches DoRequest→any unmarshalling (JSON numbers are float64).
func jsonNumberAsInt64(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	default:
		return 0
	}
}

func shouldPromptAutocreateWatch(ctx *cmdctx.Ctx) bool {
	if !console.IsBothTTY() {
		return false
	}
	switch strings.ToLower(ctx.FormatFlags.Format) {
	case "", "text":
		return true
	default:
		return false
	}
}

func startAutocreateLogWatchAfterImport(ctx *cmdctx.Ctx, importRequestID string, expected int64) error {
	listCS := ctx.Resolver.GetSpec("list", "gitops_autocreate_log")
	if listCS == nil || listCS.Endpoint == nil {
		return fmt.Errorf("no spec found for list gitops_autocreate_log")
	}
	ctx.ParentId = ctx.Id
	if ctx.FlagValues == nil {
		ctx.FlagValues = map[string]any{}
	}
	ctx.FlagValues[flagImportRequestID] = importRequestID
	fmt.Fprintln(os.Stderr, "Watching auto-create logs (Ctrl-C, timeout, or planned count to stop)...")
	return watchGitopsAutocreateLog(ctx, listCS, expected)
}

func listGitopsAutocreateLog(ctx *cmdctx.Ctx) error {
	cs := ctx.Resolver.GetSpec("list", "gitops_autocreate_log")
	if cs == nil || cs.Endpoint == nil {
		return fmt.Errorf("no spec found for list gitops_autocreate_log")
	}
	if cmdctx.GetBool(ctx.FlagValues, "list-columns") {
		w, closeW, err := format.OpenWriter(ctx.FormatFlags.OutFile)
		if err != nil {
			return err
		}
		defer closeW()
		return registry.PrintFieldTable(w, ctx.Resolver.ResolveCommandFields(cs))
	}
	watch := cmdctx.GetBool(ctx.FlagValues, flagWatch)
	pf, err := autocreateLogPagingFlags(ctx.FlagValues, watch)
	if err != nil {
		return err
	}
	if !watch {
		ctx.PagingFlags = pf
		_, err := ctx.Resolver.RunEndpoint(ctx, cs.Endpoint)
		return err
	}
	return watchGitopsAutocreateLog(ctx, cs, 0)
}

func autocreateLogPagingFlags(fv map[string]any, watch bool) (cmdctx.PagingFlags, error) {
	offset, err := parseNonNegIntFlag(fv, flagOffset)
	if err != nil {
		return cmdctx.PagingFlags{}, err
	}
	limit, err := parseNonNegIntFlag(fv, flagLimit)
	if err != nil {
		return cmdctx.PagingFlags{}, err
	}
	all := cmdctx.GetBool(fv, flagAll)
	count := cmdctx.GetBool(fv, flagCount)
	if watch {
		if offset != 0 || limit != 0 || count {
			return cmdctx.PagingFlags{}, fmt.Errorf("--watch is incompatible with --offset, --limit, and --count")
		}
		// Full walk each tick; --all is redundant but allowed.
		return cmdctx.PagingFlags{All: true}, nil
	}
	if all && (offset != 0 || limit != 0) {
		return cmdctx.PagingFlags{}, fmt.Errorf("--all is incompatible with --offset and --limit")
	}
	if count && (offset != 0 || limit != 0 || all) {
		return cmdctx.PagingFlags{}, fmt.Errorf("--count is incompatible with --offset, --limit, and --all")
	}
	return cmdctx.PagingFlags{Offset: offset, Limit: limit, All: all, Count: count}, nil
}

func parseNonNegIntFlag(fv map[string]any, name string) (int, error) {
	raw := strings.TrimSpace(cmdctx.GetString(fv, name))
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("--%s must be an integer", name)
	}
	if n < 0 {
		return 0, fmt.Errorf("--%s must be non-negative", name)
	}
	return n, nil
}

func watchGitopsAutocreateLog(ctx *cmdctx.Ctx, cs *spec.CommandSpec, expected int64) error {
	watchCtx, stopWatch := withAutocreateLogWatchDeadline(ctx)
	defer stopWatch()
	prevCtx := ctx.Context
	ctx.Context = watchCtx
	defer func() { ctx.Context = prevCtx }()

	fields := ctx.Resolver.ResolveCommandFields(cs)
	seen := map[string]string{}
	for {
		items, err := ctx.Resolver.FetchItems(ctx, cs.Endpoint, cmdctx.PagingFlags{All: true})
		if err != nil {
			return err
		}
		delta, failed := autocreateLogDelta(items, seen)
		fmt.Fprintf(os.Stderr, "autocreate-logs: total=%d new=%d failed=%d\n", len(items), len(delta), failed)
		if len(delta) > 0 {
			if err := printAutocreateLogDelta(ctx, cs, fields, delta); err != nil {
				return err
			}
		}
		if autocreateLogReachedPlan(len(items), expected) {
			fmt.Fprintf(os.Stderr, "autocreate-logs: reached planned count (%d); done\n", expected)
			return nil
		}
		select {
		case <-ctx.Context.Done():
			err := context.Cause(ctx.Context)
			if errors.Is(err, context.DeadlineExceeded) {
				fmt.Fprintln(os.Stderr, "autocreate-logs: watch stopped (timeout)")
			}
			return autocreateLogWatchDoneErr(err)
		case <-time.After(autocreateLogWatchInterval):
		}
	}
}

// autocreateLogReachedPlan: import watch only (expected>0); any status counts toward total.
func autocreateLogReachedPlan(total int, expected int64) bool {
	return expected > 0 && int64(total) >= expected
}

// autocreateLogWatchDoneErr: deadline is an intentional watch stop; cancel still fails the command.
func autocreateLogWatchDoneErr(err error) error {
	if err == nil || errors.Is(err, context.DeadlineExceeded) {
		return nil
	}
	return err
}

// withAutocreateLogWatchDeadline uses root --timeout when set; otherwise caps watch at 2m.
// Root timeout is not in FlagValues, so args are scanned only for this defaulting decision.
func withAutocreateLogWatchDeadline(ctx *cmdctx.Ctx) (context.Context, context.CancelFunc) {
	if rootTimeoutSecs(os.Args) > 0 {
		return ctx.Context, func() {}
	}
	return context.WithTimeout(ctx.Context, autocreateLogWatchDefaultStop)
}

func rootTimeoutSecs(args []string) float64 {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--timeout" && i+1 < len(args):
			f, err := strconv.ParseFloat(args[i+1], 64)
			if err == nil {
				return f
			}
		case strings.HasPrefix(a, "--timeout="):
			f, err := strconv.ParseFloat(strings.TrimPrefix(a, "--timeout="), 64)
			if err == nil {
				return f
			}
		}
	}
	return 0
}

// autocreateLogDelta returns rows that are new or whose status changed, and counts FAILED in the snapshot.
// seen maps identity → last status; identity excludes status so flips are visible.
func autocreateLogDelta(items []any, seen map[string]string) (delta []any, failed int) {
	for _, item := range items {
		key, status, ok := autocreateLogIdentity(item)
		if !ok {
			continue
		}
		if status == "FAILED" {
			failed++
		}
		prev, known := seen[key]
		if !known || prev != status {
			delta = append(delta, item)
			seen[key] = status
		}
	}
	return delta, failed
}

func autocreateLogIdentity(item any) (key, status string, ok bool) {
	m, ok := item.(map[string]any)
	if !ok {
		return "", "", false
	}
	rt := autocreateLogStringField(m, "resourceType")
	rr := autocreateLogStringField(m, "resourceRef")
	st := autocreateLogStringField(m, "status")
	ca := autocreateLogStringField(m, "createdAt")
	if rt == "" && rr == "" {
		return "", "", false
	}
	return rt + "\x00" + rr + "\x00" + ca, st, true
}

func autocreateLogStringField(m map[string]any, name string) string {
	v, ok := m[name]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatInt(int64(t), 10)
	case json.Number:
		return t.String()
	default:
		return fmt.Sprint(t)
	}
}

func printAutocreateLogDelta(ctx *cmdctx.Ctx, cs *spec.CommandSpec, fields []spec.FieldDef, delta []any) error {
	switch strings.ToLower(ctx.FormatFlags.Format) {
	case "json", "jsonl":
		w, closeW, err := format.OpenWriter(ctx.FormatFlags.OutFile)
		if err != nil {
			return err
		}
		defer closeW()
		enc := json.NewEncoder(w)
		for _, item := range delta {
			if err := enc.Encode(item); err != nil {
				return err
			}
		}
		return nil
	default:
		return ctx.Resolver.FormatList(ctx, delta, fields, cs.Endpoint.Columns)
	}
}

type importNamesFile struct {
	ArgoProjectNames []string `yaml:"argoProjectNames"`
}

func gitopsAgentImportBody(ctx *cmdctx.Ctx) (any, error) {
	fromFlag := cmdctx.GetStringSlice(ctx.FlagValues, flagArgoProjectNames)
	hasFile := cmdctx.GetString(ctx.FlagValues, flagFile) != ""
	if len(fromFlag) > 0 && hasFile {
		return nil, fmt.Errorf("use either --%s or -f, not both", flagArgoProjectNames)
	}
	var names []string
	switch {
	case hasFile:
		raw, err := cmdctx.SlurpInputFile(ctx.FlagValues)
		if err != nil {
			return nil, err
		}
		var file importNamesFile
		if err := yaml.Unmarshal([]byte(raw), &file); err != nil {
			return nil, fmt.Errorf("parsing -f import file: %w", err)
		}
		names = file.ArgoProjectNames
	case len(fromFlag) > 0:
		names = fromFlag
	default:
		return nil, fmt.Errorf("provide --%s or -f import.yaml", flagArgoProjectNames)
	}
	out := make([]string, 0, len(names))
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n != "" {
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("at least one Argo AppProject name is required")
	}
	return map[string]any{wireKeyProjectNames: out}, nil
}

func formatGitopsAgentImportSummary(w io.Writer, d cmdctx.DataAccessor) error {
	fmt.Fprintf(w, "\nImport complete for agent %s\n", d.GetString("ctx.id"))
	id := d.GetString("it.importRequestId")
	if id != "" {
		fmt.Fprintf(w, "  importRequestId: %s\n\n", id)
	}
	root, _ := d.GetData().(map[string]any)
	fmt.Fprintln(w, "  Imported:")
	writeImportCountLine(w, "applications", d.GetInt64("it.applicationCount"), asIntMap(root, "applicationPerProjectCount"))
	appSetPer := asIntMap(root, "applicationSetPerProjectCount")
	if len(appSetPer) == 0 {
		appSetPer = asIntMap(root, "appSetPerProjectCount")
	}
	writeImportCountLine(w, "application sets", d.GetInt64("it.applicationSetCount"), appSetPer)
	writeImportCountLine(w, "clusters", d.GetInt64("it.clusterCount"), asIntMap(root, "clusterPerProjectCount"))
	writeImportCountLine(w, "repositories", d.GetInt64("it.repositoryCount"), asIntMap(root, "repositoryPerProjectCount"))
	fmt.Fprintf(w, "    %-18s %d\n", "repo credentials:", d.GetInt64("it.repoCredsCount"))
	fmt.Fprintf(w, "    %-18s %d\n", "repo certificates:", d.GetInt64("it.repositoryCertificateCount"))
	fmt.Fprintf(w, "    %-18s %d\n", "GPG keys:", d.GetInt64("it.gnuPGPublicKeyCount"))

	svc := d.GetInt64("it.reconcileAppResponse.autoCreateCounts.serviceCount")
	env := d.GetInt64("it.reconcileAppResponse.autoCreateCounts.environmentCount")
	links := d.GetInt64("it.reconcileAppResponse.autoCreateCounts.clusterLinkCount")
	if svc > 0 || env > 0 || links > 0 {
		fmt.Fprintf(w, "\n  Auto-create planned (async, not final):\n")
		fmt.Fprintf(w, "    services: %d  environments: %d  cluster links: %d\n", svc, env, links)
		if id != "" {
			agent := d.GetString("ctx.id")
			fmt.Fprintf(w, "\n  Auto-create is async. Import History (Agent Details), or later:\n")
			fmt.Fprintf(w, "    harness list gitops_autocreate_log %s --import-request-id %s --watch\n", agent, id)
		}
	}
	fmt.Fprintln(w)
	return nil
}

func writeImportCountLine(w io.Writer, label string, total int64, per map[string]int64) {
	if len(per) == 0 {
		fmt.Fprintf(w, "    %-18s %d\n", label+":", total)
		return
	}
	fmt.Fprintf(w, "    %-18s %d  (%s)\n", label+":", total, formatPerProjectParen(per))
}

func formatPerProjectParen(per map[string]int64) string {
	if len(per) == 0 {
		return ""
	}
	keys := make([]string, 0, len(per))
	for k := range per {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		name := k
		if name == "" {
			name = "(none)"
		}
		parts = append(parts, fmt.Sprintf("%s=%d", name, per[k]))
	}
	return strings.Join(parts, ", ")
}

// asIntMap reads a JSON object of counts. DoRequest unmarshals into any, so
// JSON numbers land as float64 even though the API fields are int32.
func asIntMap(root map[string]any, key string) map[string]int64 {
	if root == nil {
		return nil
	}
	raw, ok := root[key].(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]int64, len(raw))
	for k, v := range raw {
		// Same number boxes as extractutil.GetInt64 after json.Unmarshal into any.
		switch n := v.(type) {
		case float64:
			out[k] = int64(n)
		case int64:
			out[k] = n
		case int:
			out[k] = int64(n)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// deleteAppProjectMappingBody checks required --set keys; DELETE sends no body.
func deleteAppProjectMappingBody(ctx *cmdctx.Ctx) (any, error) {
	if ctx.SetArgs[setArgOrg] == "" {
		return nil, fmt.Errorf("--set %s=<harness-org> is required", setArgOrg)
	}
	if ctx.SetArgs[setArgProject] == "" {
		return nil, fmt.Errorf("--set %s=<harness-project> is required", setArgProject)
	}
	return nil, nil
}

// updateAppProjectMappingBody builds V1 PUT body (autocreate toggle only; org/project are lookup keys).
func updateAppProjectMappingBody(ctx *cmdctx.Ctx) (any, error) {
	if len(ctx.IdParts) < 2 || ctx.IdParts[1] == "" {
		return nil, fmt.Errorf("expected <agent/argoproject>")
	}
	org := ctx.SetArgs[setArgOrg]
	if org == "" {
		return nil, fmt.Errorf("--set %s=<harness-org> is required", setArgOrg)
	}
	project := ctx.SetArgs[setArgProject]
	if project == "" {
		return nil, fmt.Errorf("--set %s=<harness-project> is required", setArgProject)
	}
	autoRaw := ctx.SetArgs[setArgAutoCreateServiceEnv]
	if autoRaw != setArgTrue && autoRaw != setArgFalse {
		return nil, fmt.Errorf("--set %s=true|false is required", setArgAutoCreateServiceEnv)
	}
	return wrapAppProjMap(map[string]any{
		ctx.IdParts[1]: map[string]any{
			wireKeyOrgIdentifier:        org,
			wireKeyProjectIdentifier:    project,
			wireKeyAutoCreateServiceEnv: autoRaw == setArgTrue,
		},
	}), nil
}

// mappingsFile is the -f create shape (plan §6.1). Not the wire body — body_fn folds to appProjMap.
type mappingsFile struct {
	Mappings []mappingRow `yaml:"mappings"`
}

type mappingRow struct {
	ArgoProject          string `yaml:"argoproject"`
	Org                  string `yaml:"org"`
	Project              string `yaml:"project"`
	AutoCreateServiceEnv bool   `yaml:"autoCreateServiceEnv"`
}

// createAppProjectMappingBody builds V1 POST body for create gitops_app_project_mapping.
// Input: --set (one row) OR -f mappings.yaml (many rows). Not both.
// Do not set endpoint file_body: that would POST the file as-is and skip this fn.
func createAppProjectMappingBody(ctx *cmdctx.Ctx) (any, error) {
	hasFile := cmdctx.GetString(ctx.FlagValues, flagFile) != ""
	hasSet := ctx.SetArgs[setArgArgoProject] != "" || ctx.SetArgs[setArgOrg] != "" ||
		ctx.SetArgs[setArgProject] != "" || ctx.SetArgs[setArgAutoCreateServiceEnv] != ""
	if hasFile && hasSet {
		return nil, fmt.Errorf("use either -f mappings.yaml or --set, not both")
	}
	if hasFile {
		return appProjMapBodyFromFile(ctx)
	}
	argo := ctx.SetArgs[setArgArgoProject]
	if argo == "" {
		return nil, fmt.Errorf("--set %s=<argo-project-name> is required", setArgArgoProject)
	}
	org := ctx.SetArgs[setArgOrg]
	if org == "" {
		return nil, fmt.Errorf("--set %s=<harness-org> is required", setArgOrg)
	}
	project := ctx.SetArgs[setArgProject]
	if project == "" {
		return nil, fmt.Errorf("--set %s=<harness-project> is required", setArgProject)
	}
	autoCreate := ctx.SetArgs[setArgAutoCreateServiceEnv] == setArgTrue
	return wrapAppProjMap(map[string]any{
		argo: map[string]any{
			wireKeyOrgIdentifier:        org,
			wireKeyProjectIdentifier:    project,
			wireKeyAutoCreateServiceEnv: autoCreate,
		},
	}), nil
}

func appProjMapBodyFromFile(ctx *cmdctx.Ctx) (any, error) {
	raw, err := cmdctx.SlurpInputFile(ctx.FlagValues)
	if err != nil {
		return nil, err
	}
	var file mappingsFile
	if err := yaml.Unmarshal([]byte(raw), &file); err != nil {
		return nil, fmt.Errorf("parsing -f mappings file: %w", err)
	}
	if len(file.Mappings) == 0 {
		return nil, fmt.Errorf("%s list is empty", yamlKeyMappings)
	}
	appProjMap := make(map[string]any, len(file.Mappings))
	for i, row := range file.Mappings {
		if row.ArgoProject == "" {
			return nil, fmt.Errorf("%s[%d]: %s is required", yamlKeyMappings, i, setArgArgoProject)
		}
		if row.Org == "" {
			return nil, fmt.Errorf("%s[%d]: %s is required", yamlKeyMappings, i, setArgOrg)
		}
		if row.Project == "" {
			return nil, fmt.Errorf("%s[%d]: %s is required", yamlKeyMappings, i, setArgProject)
		}
		if _, dup := appProjMap[row.ArgoProject]; dup {
			return nil, fmt.Errorf("duplicate argo project: %s", row.ArgoProject)
		}
		appProjMap[row.ArgoProject] = map[string]any{
			wireKeyOrgIdentifier:        row.Org,
			wireKeyProjectIdentifier:    row.Project,
			wireKeyAutoCreateServiceEnv: row.AutoCreateServiceEnv,
		}
	}
	return wrapAppProjMap(appProjMap), nil
}

func wrapAppProjMap(appProjMap map[string]any) map[string]any {
	return map[string]any{wireKeyAppProjMap: appProjMap}
}

// installInput mirrors the subset of v1AgentYamlQuery exposed via -f install.yaml.
type installInput struct {
	Namespace                  string         `yaml:"namespace"`
	DisasterRecoveryIdentifier string         `yaml:"disasterRecoveryIdentifier"`
	SkipCrds                   bool           `yaml:"skipCrds"`
	CaData                     string         `yaml:"caData"`
	PrivateKey                 string         `yaml:"privateKey"`
	Proxy                      map[string]any `yaml:"proxy"`
	ArgocdSettings             map[string]any `yaml:"argocdSettings"`
}

// executeAgentInstall implements "execute gitops_agent:install". It fetches the
// Helm override.yaml (or, with --method yaml, the plain k8s manifest) for an
// existing agent and prints the commands to install it. It never touches a
// cluster itself. This is a workflow (not a spec-only endpoint) because these
// endpoints respond with a literal YAML string body, which client.DoRequest's
// unconditional json.Unmarshal cannot decode — client.DoRaw is required instead.
func executeAgentInstall(ctx *cmdctx.Ctx) error {
	agentID := ctx.Id
	if agentID == "" {
		return errors.New("execute gitops_agent:install requires a positional <id> argument")
	}
	method := cmdctx.GetString(ctx.FlagValues, "method")
	if method == "" {
		method = "helm"
	}
	if method != "helm" && method != "yaml" {
		return fmt.Errorf("invalid --method %q: must be \"helm\" or \"yaml\"", method)
	}

	cs := ctx.Resolver.GetSpec("get", "gitops_agent")
	if cs == nil || cs.Endpoint == nil {
		return errors.New("get gitops_agent command spec not found")
	}
	agentResp, err := registry.CallEndpoint(ctx, cs.Endpoint)
	if err != nil {
		return fmt.Errorf("fetching agent %q: %w (has it been created with 'harness create gitops_agent'?)", agentID, err)
	}
	namespace := ""
	if m, ok := agentResp.(map[string]any); ok {
		if md, ok := m["metadata"].(map[string]any); ok {
			namespace, _ = md["namespace"].(string)
		}
	}

	var input installInput
	if filePath := cmdctx.GetString(ctx.FlagValues, "file"); filePath != "" {
		body, err := cmdctx.SlurpInputFile(ctx.FlagValues)
		if err != nil {
			return err
		}
		if err := yaml.Unmarshal([]byte(body), &input); err != nil {
			return fmt.Errorf("parsing -f install file: %w", err)
		}
		if input.Namespace != "" {
			namespace = input.Namespace
		}
	}
	if namespace == "" {
		return fmt.Errorf("namespace is required: set it via -f install.yaml, or it must already be set on the agent (see 'harness get gitops_agent %s')", agentID)
	}

	a := *ctx.Auth
	switch ctx.Level {
	case "org":
		a.ProjectID = ""
	case "account":
		a.OrgID, a.ProjectID = "", ""
	}
	reqBody := map[string]any{
		"accountIdentifier": a.AccountID,
		"orgIdentifier":     a.OrgID,
		"projectIdentifier": a.ProjectID,
		"agentIdentifier":   agentID,
		"namespace":         namespace,
	}
	if input.SkipCrds {
		reqBody["skipCrds"] = true
	}
	for k, v := range map[string]string{"disasterRecoveryIdentifier": input.DisasterRecoveryIdentifier, "caData": input.CaData, "privateKey": input.PrivateKey} {
		if v != "" {
			reqBody[k] = v
		}
	}
	if input.Proxy != nil {
		reqBody["proxy"] = input.Proxy
	}
	if input.ArgocdSettings != nil {
		reqBody["argocdSettings"] = input.ArgocdSettings
	}
	outPath := cmdctx.GetString(ctx.FlagValues, "output_file")
	if outPath == "" {
		return fmt.Errorf("output file is required: set it via --output_file")
	}
	if !strings.HasSuffix(outPath, ".yaml") && !strings.HasSuffix(outPath, ".yaml.txt") {
		return fmt.Errorf("output file must end with .yaml or .yaml.txt")
	}

	path := fmt.Sprintf("/gitops/api/v1/agents/%s/helm-overrides", agentID)

	c := client.New(ctx)
	resp, err := c.DoRaw(client.Request{Method: "POST", Path: path, Body: reqBody})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading API response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("API error %d: %s", resp.StatusCode, client.APIErrorMessage(resp.StatusCode, respBody))
	}
	// Despite the OpenAPI doc annotating these routes as application/yaml, the
	// gRPC-gateway actually wraps the string response as JSON: {"value": "..."}.
	var wrapped struct {
		Value string `json:"value"`
	}
	content := respBody
	if json.Unmarshal(respBody, &wrapped) == nil && wrapped.Value != "" {
		content = []byte(wrapped.Value)
	}
	if err := os.WriteFile(outPath, content, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", outPath, err)
	}

	fmt.Printf("\nWrote %s install artifact to %s\n\nInstall the agent with:\n", method, outPath)
	fmt.Println(" Connect to your Kubernetes cluster and install the agent with:")
	if method == "helm" {
		fmt.Println("  helm repo add gitops-agent https://harness.github.io/gitops-helm/")
		fmt.Println("  helm repo update gitops-agent")
		fmt.Printf("  helm install argocd gitops-agent/gitops-helm --values %s --namespace %s\n", outPath, namespace)
	} else {
		fmt.Printf("  kubectl apply -f %s -n %s\n", outPath, namespace)
	}
	fmt.Printf("\nAfter installing, check status with: harness get gitops_agent %s\n", agentID)
	return nil
}
