// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jedib0t/go-pretty/v6/table"
	"go.yaml.in/yaml/v3"

	"github.com/harness/cli/modules/har/pkg/har/genapi/ar_v3"
	"github.com/harness/cli/v3/pkg/auth"
	"github.com/harness/cli/v3/pkg/cmdctx"
)

const executeArtifactFirewallScanHandlerID = "execute_artifact_firewall_scan"

// ---- API helpers ----

func harV3URL(apiUrl, path string) string {
	return apiUrl + "/gateway/har/api/v3" + path
}

// newFirewallClient builds an ar_v3 client for the firewall scan endpoints,
// wired with the same auth header and base URL used across the firewall handlers.
func newFirewallClient(hc *http.Client, a *auth.ResolvedAuth) (*ar_v3.ClientWithResponses, error) {
	return ar_v3.NewClientWithResponses(harV3URL(a.APIUrl, ""), ar_v3.WithHTTPClient(hc), ar_v3.WithRequestEditorFn(func(_ context.Context, req *http.Request) error {
		a.SetAuthHeader(req)
		return nil
	}))
}

// optionalStrPtr returns nil for an empty string, otherwise a pointer to s.
// Used for the optional org/project identifier query params on the ar_v3 client.
func optionalStrPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// applyLevelScope returns a copy of cmdCtx.Auth with OrgID/ProjectID stripped per
// cmdCtx.Level ("org" clears ProjectID, "account" clears OrgID and ProjectID),
// mirroring the scope-handling in pkg/registry/endpoint.go's callEndpointFull.
// firewall_scan is a workflow-handler command, so it never goes through that
// endpoint layer and must apply --level itself.
func applyLevelScope(cmdCtx *cmdctx.Ctx) *auth.ResolvedAuth {
	a := cmdCtx.Auth
	switch cmdCtx.Level {
	case "org":
		copy := *a
		copy.ProjectID = ""
		a = &copy
	case "account":
		copy := *a
		copy.OrgID = ""
		copy.ProjectID = ""
		a = &copy
	}
	return a
}

// getRegistryUUID fetches the registry via the HAR v1 API and returns its UUID.
func getRegistryUUID(ctx context.Context, hc *http.Client, a *auth.ResolvedAuth, registryID string) (string, error) {
	apiUrl, accountID, orgID, projectID := a.APIUrl, a.AccountID, a.OrgID, a.ProjectID
	scope := accountID
	if orgID != "" {
		scope += "/" + orgID
		if projectID != "" {
			scope += "/" + projectID
		}
	}
	u, err := url.Parse(apiUrl + fmt.Sprintf("/har/api/v1/registry/%s/%s/+", scope, registryID))
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("accountIdentifier", accountID)
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return "", err
	}
	a.SetAuthHeader(req)

	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("registry lookup error %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var envelope struct {
		Data struct {
			Uuid *string `json:"uuid"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return "", fmt.Errorf("parsing registry response: %w", err)
	}
	if envelope.Data.Uuid == nil || *envelope.Data.Uuid == "" {
		return "", fmt.Errorf("registry %q: UUID not found in response", registryID)
	}
	return *envelope.Data.Uuid, nil
}

// ---- handler ----

func executeArtifactFirewallScanHandler(cmdCtx *cmdctx.Ctx) error {
	a := applyLevelScope(cmdCtx)

	if len(cmdCtx.IdParts) < 3 {
		return fmt.Errorf("artifact:firewall_scan requires <registry/name/version>, got %q", cmdCtx.Id)
	}
	registryID := cmdCtx.IdParts[0]
	packageName := cmdCtx.IdParts[1]
	version := cmdCtx.IdParts[2]

	ctx := cmdCtx.Context
	if ctx == nil {
		ctx = context.Background()
	}
	hc := &http.Client{Timeout: 60 * time.Second}

	fmt.Printf("Fetching registry details for: %s\n", registryID)
	registryUUID, err := getRegistryUUID(ctx, hc, a, registryID)
	if err != nil {
		return fmt.Errorf("fetching registry: %w", err)
	}
	fmt.Printf("Registry UUID: %s\n", registryUUID)
	registryUUIDParsed, err := uuid.Parse(registryUUID)
	if err != nil {
		return fmt.Errorf("parsing registry UUID %q: %w", registryUUID, err)
	}

	client, err := newFirewallClient(hc, a)
	if err != nil {
		return fmt.Errorf("building firewall client: %w", err)
	}

	useAsync := cmdctx.GetBool(cmdCtx.FlagValues, "async")
	evaluator := newFirewallEvaluator(firewallEvaluatorParams{
		client:       client,
		accountID:    a.AccountID,
		orgID:        a.OrgID,
		projectID:    a.ProjectID,
		registryUUID: registryUUIDParsed,
		skipCache:    true,
	}, useAsync)

	fmt.Printf("Evaluating %s@%s (mode=%s)\n", packageName, version, evaluator.Mode())
	result, err := evaluator.Evaluate(ctx, []dependency{{Name: packageName, Version: version}})
	if err != nil {
		return err
	}
	if len(result.results) == 0 {
		fmt.Println("No scan results returned.")
		return nil
	}

	scan := result.results[0]

	fmt.Println()
	fmt.Println("Scan Result")
	fmt.Printf("  Package:           %s\n", packageName)
	fmt.Printf("  Version:           %s\n", version)
	fmt.Printf("  Evaluation Status: %s\n", displayVal(scan.ScanStatus))
	fmt.Printf("  Evaluation ID:     %s\n", displayVal(scan.ScanID))
	fmt.Println()
	switch scan.ScanStatus {
	case "BLOCKED":
		fmt.Println("BLOCKED: This artifact version is blocked by the firewall.")
	case "WARN":
		fmt.Println("WARN: This artifact version has firewall warnings.")
	case "ALLOWED":
		fmt.Println("ALLOWED: This artifact version is allowed by the firewall.")
	}

	if cmdctx.GetString(cmdCtx.FlagValues, "format") == "json" {
		return printExplainJSON(registryID, packageName, version, scan)
	}

	if !useAsync {
		if len(result.details) > 0 && result.details[0] != nil {
			printScanDetails(syncScanDetails(result.details[0]))
		}
		return nil
	}

	if scan.ScanID == "" {
		return nil
	}
	fmt.Println()
	fmt.Println("Fetching detailed scan information...")
	detailResp, err := client.GetArtifactScanDetailsWithResponse(ctx, scan.ScanID, &ar_v3.GetArtifactScanDetailsParams{
		AccountIdentifier: a.AccountID,
	})
	if err != nil {
		fmt.Printf("  (could not fetch scan details: %v)\n", err)
		return nil
	}
	if detailResp.JSON200 != nil && detailResp.JSON200.Data != nil {
		printScanDetails(detailResp.JSON200.Data)
	}
	return nil
}

func printExplainJSON(registryID, packageName, version string, scan auditScanResult) error {
	b, err := json.MarshalIndent(map[string]string{
		"registry":   registryID,
		"package":    packageName,
		"version":    version,
		"scanStatus": scan.ScanStatus,
		"scanId":     scan.ScanID,
	}, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(b))
	return nil
}

func syncScanDetails(details *ar_v3.ArtifactScanDetailsV3) *ar_v3.ArtifactScanDetails {
	converted := &ar_v3.ArtifactScanDetails{
		CreatedAt:               details.CreatedAt,
		FixVersionDetails:       details.FixVersionDetails,
		LastEvaluatedAt:         details.LastEvaluatedAt,
		PackageName:             details.PackageName,
		PolicySetFailureDetails: details.PolicySetFailureDetails,
		ScanStatus:              ar_v3.ArtifactScanDetailsScanStatus(details.ScanStatus),
		Version:                 details.Version,
		VersionId:               details.VersionId,
	}
	if details.Id != nil {
		converted.Id = *details.Id
	}
	if details.PackageType != nil {
		converted.PackageType = *details.PackageType
	}
	if details.RegistryId != nil {
		converted.RegistryId = details.RegistryId
	}
	if details.RegistryName != nil {
		converted.RegistryName = *details.RegistryName
	}
	return converted
}

// ---- output helpers ----

func displayVal(s string) string {
	if s == "" {
		return "(not set)"
	}
	return s
}

func fmtTimestampMs(s string) string {
	if s == "" {
		return "(not set)"
	}
	ms, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return s
	}
	return time.UnixMilli(ms).Format("2006-01-02 15:04:05 MST")
}

func printScanDetails(d *ar_v3.ArtifactScanDetails) {
	fmt.Println()
	fmt.Println("Evaluation Details:")
	fmt.Println(strings.Repeat("=", 60))

	if d.LastEvaluatedAt != nil && *d.LastEvaluatedAt != "" {
		fmt.Printf("Last Evaluated: %s\n", fmtTimestampMs(*d.LastEvaluatedAt))
	}

	// Show fix info if any security violation present.
	hasSecViolation := false
	if d.PolicySetFailureDetails != nil {
		for _, ps := range *d.PolicySetFailureDetails {
			for _, f := range ps.PolicyFailureDetails {
				if f.Category == ar_v3.Security {
					hasSecViolation = true
					break
				}
			}
		}
	}
	if hasSecViolation && d.FixVersionDetails != nil {
		fv := d.FixVersionDetails
		fmt.Println()
		fmt.Println("Security Fix Information:")
		fmt.Printf("  Fix Available:   %v\n", fv.FixVersionAvailable)
		fmt.Printf("  Current Version: %s\n", fv.CurrentVersion)
		if fv.FixVersion != nil {
			fmt.Printf("  Fix Version:     %s\n", *fv.FixVersion)
		}
	}

	if d.PolicySetFailureDetails == nil || len(*d.PolicySetFailureDetails) == 0 {
		return
	}

	fmt.Println()
	fmt.Println("Policy Set Violations:")

	for psIdx, ps := range *d.PolicySetFailureDetails {
		fmt.Println()
		fmt.Printf("Policy Set %d: %s\n", psIdx+1, ps.PolicySetName)
		fmt.Printf("Policy Set Ref: %s\n", ps.PolicySetRef)
		fmt.Println(strings.Repeat("-", 60))

		for i, f := range ps.PolicyFailureDetails {
			fmt.Printf("\n  %d.%d %s\n", psIdx+1, i+1, f.Category)
			fmt.Printf("      Policy Name: %s\n", f.PolicyName)
			fmt.Printf("      Policy Ref:  %s\n", f.PolicyRef)

			switch f.Category {
			case ar_v3.Security:
				if sc, err := f.AsSecurityPolicyFailureDetailConfig(); err == nil && len(sc.Vulnerabilities) > 0 {
					fmt.Println("\n      Vulnerabilities:")
					fmt.Printf("        %-24s %10s %15s\n", "CVE ID", "CVSS Score", "CVSS Threshold")
					fmt.Printf("        %-24s %10s %15s\n", strings.Repeat("-", 24), strings.Repeat("-", 10), strings.Repeat("-", 15))
					for _, v := range sc.Vulnerabilities {
						fmt.Printf("        %-24s %10.1f %15.1f\n", v.CveId, v.CvssScore, v.CvssThreshold)
					}
				}
			case ar_v3.License:
				if lc, err := f.AsLicensePolicyFailureDetailConfig(); err == nil {
					fmt.Printf("\n      Blocked License:  %s\n", lc.BlockedLicense)
					if len(lc.AllowedLicenses) > 0 {
						fmt.Printf("      Allowed Licenses: %s\n", strings.Join(lc.AllowedLicenses, ", "))
					}
				}
			case ar_v3.PackageAge:
				if pa, err := f.AsPackageAgeViolationPolicyFailureDetailConfig(); err == nil {
					fmt.Printf("\n      Published On:          %s\n", fmtTimestampMs(pa.PublishedOn))
					fmt.Printf("      Package Age Threshold: %s\n", pa.PackageAgeThreshold)
				}
			}
		}
	}
}

// ---- registry:firewall_scan (audit) handler ----

const executeRegistryFirewallScanHandlerID = "execute_registry_firewall_scan"

type dependency struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Parent  string `json:"parent,omitempty"`
}

type auditScanResult struct {
	PackageName string `json:"packageName"`
	Version     string `json:"version"`
	ScanID      string `json:"scanId"`
	ScanStatus  string `json:"scanStatus"`
}

type firewallBatchOutcome struct {
	index   int
	results []auditScanResult
	err     error
}

func evaluateFirewallBatches(
	ctx context.Context,
	evaluator firewallEvaluator,
	deps []dependency,
	batchSize int,
) ([]auditScanResult, error) {
	if batchSize <= 0 {
		batchSize = 20
	}
	if batchSize > firewallMaxBatchSize {
		batchSize = firewallMaxBatchSize
	}

	totalBatches := (len(deps) + batchSize - 1) / batchSize
	workers := 3
	if evaluator.Mode() == "async" {
		workers = 1
	}

	outcomes := make(chan firewallBatchOutcome, totalBatches)
	semaphore := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for i := 0; i < totalBatches; i++ {
		start := i * batchSize
		end := start + batchSize
		if end > len(deps) {
			end = len(deps)
		}
		batch := deps[start:end]

		wg.Add(1)
		go func(index int, items []dependency) {
			defer wg.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				outcomes <- firewallBatchOutcome{
					index:   index,
					results: unknownAuditResults(items),
					err:     context.Cause(ctx),
				}
				return
			}

			result, err := evaluator.Evaluate(ctx, items)
			if err != nil {
				outcomes <- firewallBatchOutcome{
					index:   index,
					results: unknownAuditResults(items),
					err:     err,
				}
				return
			}
			outcomes <- firewallBatchOutcome{index: index, results: result.results}
		}(i, batch)
	}

	go func() {
		wg.Wait()
		close(outcomes)
	}()

	results := make([]auditScanResult, 0, len(deps))
	var firstErr error
	for outcome := range outcomes {
		if outcome.err != nil && firstErr == nil {
			firstErr = outcome.err
		}
		if outcome.err != nil {
			fmt.Fprintf(
				os.Stderr,
				"Batch %d/%d failed: %v; marked %d result(s) UNKNOWN\n",
				outcome.index+1,
				totalBatches,
				outcome.err,
				len(outcome.results),
			)
		} else {
			fmt.Fprintf(
				os.Stderr,
				"Batch %d/%d complete (%d packages)\n",
				outcome.index+1,
				totalBatches,
				len(outcome.results),
			)
		}
		results = append(results, outcome.results...)
	}
	return results, firstErr
}

func unknownAuditResults(deps []dependency) []auditScanResult {
	results := make([]auditScanResult, 0, len(deps))
	for _, dep := range deps {
		results = append(results, auditScanResult{
			PackageName: dep.Name,
			Version:     dep.Version,
			ScanStatus:  "UNKNOWN",
		})
	}
	return results
}

func executeRegistryFirewallScanHandler(cmdCtx *cmdctx.Ctx) error {
	a := applyLevelScope(cmdCtx)
	registryID := cmdCtx.Id
	filePath := cmdctx.GetString(cmdCtx.FlagValues, "lockfile")

	ctx := cmdCtx.Context
	if ctx == nil {
		ctx = context.Background()
	}
	hc := &http.Client{Timeout: 60 * time.Second}

	// 1. Registry UUID.
	fmt.Printf("Fetching registry details for: %s\n", registryID)
	registryUUID, err := getRegistryUUID(ctx, hc, a, registryID)
	if err != nil {
		return fmt.Errorf("fetching registry: %w", err)
	}
	fmt.Printf("Registry UUID: %s\n", registryUUID)
	registryUUIDParsed, err := uuid.Parse(registryUUID)
	if err != nil {
		return fmt.Errorf("parsing registry UUID %q: %w", registryUUID, err)
	}

	client, err := newFirewallClient(hc, a)
	if err != nil {
		return fmt.Errorf("building firewall client: %w", err)
	}

	// 2. Parse lock file.
	fmt.Printf("Parsing dependency file: %s\n", filepath.Base(filePath))
	deps, err := parseLockFile(filePath)
	if err != nil {
		return fmt.Errorf("parsing lock file: %w", err)
	}
	if len(deps) == 0 {
		return fmt.Errorf(
			"no dependencies parsed from %s; the file may be empty or in an unsupported format",
			filepath.Base(filePath),
		)
	}
	sort.Slice(deps, func(i, j int) bool { return deps[i].Name < deps[j].Name })
	fmt.Printf("Found %d dependencies.\n", len(deps))

	useAsync := cmdctx.GetBool(cmdCtx.FlagValues, "async")
	evaluator := newFirewallEvaluator(firewallEvaluatorParams{
		client:       client,
		accountID:    a.AccountID,
		orgID:        a.OrgID,
		projectID:    a.ProjectID,
		registryUUID: registryUUIDParsed,
		skipCache:    true,
	}, useAsync)
	batchSize := cmdctx.GetInt(cmdCtx.FlagValues, "batch-size")
	fmt.Printf("Evaluating dependencies (mode=%s, batch-size=%d).\n", evaluator.Mode(), batchSize)

	allResults, batchErr := evaluateFirewallBatches(ctx, evaluator, deps, batchSize)
	sort.Slice(allResults, func(i, j int) bool { return allResults[i].PackageName < allResults[j].PackageName })
	if err := printAuditResults(cmdCtx, allResults); err != nil {
		return err
	}
	if batchErr != nil {
		return fmt.Errorf("one or more firewall evaluation batches failed: %w", batchErr)
	}
	return nil
}

func printAuditResults(cmdCtx *cmdctx.Ctx, results []auditScanResult) error {
	var blocked, warn, allowed, unknown int
	for _, r := range results {
		switch r.ScanStatus {
		case "BLOCKED":
			blocked++
		case "WARN":
			warn++
		case "ALLOWED":
			allowed++
		default:
			unknown++
		}
	}

	fmt.Println()
	fmt.Printf("Scan Results (%d dependencies):\n", len(results))
	if blocked > 0 {
		fmt.Printf("  BLOCKED:  %d\n", blocked)
	}
	if warn > 0 {
		fmt.Printf("  WARN:     %d\n", warn)
	}
	if allowed > 0 {
		fmt.Printf("  ALLOWED:  %d\n", allowed)
	}
	if unknown > 0 {
		fmt.Printf("  UNKNOWN:  %d\n", unknown)
	}
	fmt.Println()

	// JSON output when requested.
	if cmdctx.GetString(cmdCtx.FlagValues, "format") == "json" {
		b, err := json.MarshalIndent(results, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(b))
		return nil
	}

	// Table.
	tw := table.NewWriter()
	tw.SetOutputMirror(os.Stdout)
	tw.AppendHeader(table.Row{"Package", "Version", "Status"})
	for _, r := range results {
		tw.AppendRow(table.Row{r.PackageName, r.Version, r.ScanStatus})
	}
	tw.Render()
	return nil
}

// ---- lock file parsers (ported from old-hc fw_audit.go) ----

func parseLockFile(filePath string) ([]dependency, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}
	name := filepath.Base(filePath)
	switch {
	case name == "package.json":
		return parsePackageJSON(data)
	case strings.HasSuffix(name, "package-lock.json"):
		return parsePackageLock(data)
	case strings.HasSuffix(name, "pnpm-lock.yaml"):
		return parsePnpmLock(data)
	case strings.HasSuffix(name, "yarn.lock"):
		return parseYarnLock(data)
	case strings.HasSuffix(name, "requirements.txt"):
		return parseRequirementsTxt(data)
	case strings.HasSuffix(name, "pyproject.toml"):
		return parsePyProjectToml(data)
	case name == "Pipfile.lock":
		return parsePipfileLock(data)
	case name == "poetry.lock":
		return parsePoetryLock(data)
	case strings.HasSuffix(name, "pom.xml"):
		return parsePomXml(data)
	case strings.HasSuffix(name, "build.gradle"), strings.HasSuffix(name, "build.gradle.kts"):
		return parseBuildGradle(data)
	case name == "packages.lock.json":
		return nugetParseLockFile(filePath)
	default:
		return nil, fmt.Errorf("unsupported file %q (supported: package.json, package-lock.json, pnpm-lock.yaml, yarn.lock, requirements.txt, pyproject.toml, Pipfile.lock, poetry.lock, pom.xml, build.gradle, packages.lock.json)", name)
	}
}

func parsePackageJSON(data []byte) ([]dependency, error) {
	var pj struct {
		Dependencies         map[string]string `json:"dependencies"`
		DevDependencies      map[string]string `json:"devDependencies"`
		PeerDependencies     map[string]string `json:"peerDependencies"`
		OptionalDependencies map[string]string `json:"optionalDependencies"`
	}
	if err := json.Unmarshal(data, &pj); err != nil {
		return nil, fmt.Errorf("parsing package.json: %w", err)
	}
	seen := map[string]bool{}
	var deps []dependency
	cleanVer := func(v string) string {
		v = strings.TrimSpace(v)
		for _, pfx := range []string{"^", "~", ">=", ">", "<=", "<", "="} {
			v = strings.TrimPrefix(v, pfx)
		}
		if i := strings.Index(v, " "); i != -1 {
			v = v[:i]
		}
		return v
	}
	for _, m := range []map[string]string{pj.Dependencies, pj.DevDependencies, pj.PeerDependencies, pj.OptionalDependencies} {
		for name, ver := range m {
			if seen[name] {
				continue
			}
			seen[name] = true
			deps = append(deps, dependency{Name: name, Version: cleanVer(ver)})
		}
	}
	return deps, nil
}

func parsePackageLock(data []byte) ([]dependency, error) {
	var lf struct {
		LockfileVersion int `json:"lockfileVersion"`
		Dependencies    map[string]struct {
			Version string `json:"version"`
		} `json:"dependencies"`
		Packages map[string]struct {
			Version string `json:"version"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(data, &lf); err != nil {
		return nil, fmt.Errorf("parsing package-lock.json: %w", err)
	}
	seen := map[string]bool{}
	var deps []dependency
	if lf.LockfileVersion >= 2 && len(lf.Packages) > 0 {
		for path, pkg := range lf.Packages {
			if path == "" {
				continue
			}
			name := strings.TrimPrefix(path, "node_modules/")
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			deps = append(deps, dependency{Name: name, Version: pkg.Version})
		}
	} else {
		for name, dep := range lf.Dependencies {
			if seen[name] {
				continue
			}
			seen[name] = true
			deps = append(deps, dependency{Name: name, Version: dep.Version})
		}
	}
	return deps, nil
}

func parsePnpmLock(data []byte) ([]dependency, error) {
	var lf struct {
		Packages map[string]struct{} `yaml:"packages"`
	}
	if err := yaml.Unmarshal(data, &lf); err != nil {
		return nil, fmt.Errorf("parsing pnpm-lock.yaml: %w", err)
	}
	seen := map[string]bool{}
	var deps []dependency
	for pkgPath := range lf.Packages {
		parts := strings.Split(pkgPath, "/")
		var name, version string
		if strings.HasPrefix(pkgPath, "@") && len(parts) >= 2 {
			name = parts[0] + "/" + parts[1]
			if len(parts) > 2 {
				version = strings.TrimPrefix(parts[2], "@")
			}
		} else if len(parts) >= 1 {
			np := strings.Split(parts[0], "@")
			if len(np) >= 2 {
				name, version = np[0], np[1]
			} else {
				name = parts[0]
			}
		}
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		deps = append(deps, dependency{Name: name, Version: version})
	}
	return deps, nil
}

func parseYarnLock(data []byte) ([]dependency, error) {
	seen := map[string]bool{}
	var deps []dependency
	var curPkg, curVer string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(line, " ") && strings.Contains(line, "@") && strings.HasSuffix(line, ":") {
			pkgLine := strings.TrimSuffix(line, ":")
			first := strings.TrimSpace(strings.Split(pkgLine, ",")[0])
			if at := strings.LastIndex(first, "@"); at > 0 {
				curPkg = strings.Trim(first[:at], "\"")
			} else {
				curPkg = strings.Trim(first, "\"")
			}
			curVer = ""
		} else if strings.HasPrefix(line, "version ") && curPkg != "" {
			curVer = strings.Trim(strings.TrimPrefix(line, "version "), "\"")
			if !seen[curPkg] {
				seen[curPkg] = true
				deps = append(deps, dependency{Name: curPkg, Version: curVer})
			}
			curPkg = ""
		}
	}
	return deps, nil
}

func parseRequirementsTxt(data []byte) ([]dependency, error) {
	seen := map[string]bool{}
	var deps []dependency
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "-") ||
			strings.HasPrefix(line, "git+") || strings.HasPrefix(line, "http") {
			continue
		}
		var name, ver string
		for _, sep := range []string{"==", ">=", "<=", "~=", ">", "<"} {
			if strings.Contains(line, sep) {
				parts := strings.SplitN(line, sep, 2)
				name = strings.TrimSpace(parts[0])
				ver = strings.TrimSpace(parts[1])
				if i := strings.Index(ver, ","); i != -1 {
					ver = ver[:i]
				}
				if i := strings.Index(ver, ";"); i != -1 {
					ver = ver[:i]
				}
				ver = strings.TrimSpace(ver)
				break
			}
		}
		if name == "" {
			name = line
			ver = "latest"
		}
		if i := strings.Index(name, "["); i != -1 {
			name = name[:i]
		}
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		deps = append(deps, dependency{Name: name, Version: ver})
	}
	return deps, nil
}

func parsePyProjectToml(data []byte) ([]dependency, error) {
	seen := map[string]bool{}
	var deps []dependency
	lines := strings.Split(string(data), "\n")
	inDeps, inOptional, inPoetry := false, false, false
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "[") {
			inDeps, inOptional, inPoetry = false, false, false
			if t == "[tool.poetry.dependencies]" {
				inPoetry = true
			} else if strings.HasPrefix(t, "[project.optional-dependencies") {
				inOptional = true
			}
			continue
		}
		if t == "dependencies = [" {
			inDeps = true
			continue
		}
		if t == "]" {
			inDeps, inOptional = false, false
			continue
		}
		var name, ver string
		if inPoetry {
			if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "python") {
				continue
			}
			if strings.Contains(t, "=") {
				parts := strings.SplitN(t, "=", 2)
				name = strings.TrimSpace(parts[0])
				vp := strings.TrimSpace(parts[1])
				vp = strings.Trim(vp, "\"'^~>=<")
				if strings.HasPrefix(vp, "{") {
					ver = "latest"
					if i := strings.Index(vp, "version"); i != -1 {
						sub := vp[i:]
						if qi := strings.Index(sub, "\""); qi != -1 {
							if qi2 := strings.Index(sub[qi+1:], "\""); qi2 != -1 {
								ver = sub[qi+1 : qi+1+qi2]
							}
						}
					}
				} else {
					ver = strings.Trim(vp, "\"'^~>=<,}")
				}
			}
		} else if inDeps || inOptional {
			if t == "" || strings.HasPrefix(t, "#") {
				continue
			}
			dl := strings.Trim(t, "\",")
			for _, sep := range []string{"==", ">=", "~="} {
				if strings.Contains(dl, sep) {
					parts := strings.SplitN(dl, sep, 2)
					name = strings.TrimSpace(parts[0])
					ver = strings.TrimSpace(parts[1])
					if i := strings.Index(ver, ","); i != -1 {
						ver = ver[:i]
					}
					break
				}
			}
			if name == "" {
				name = dl
				ver = "latest"
			}
			if i := strings.Index(ver, ";"); i != -1 {
				ver = strings.TrimSpace(ver[:i])
			}
			ver = strings.Trim(ver, "\"',")
		}
		if i := strings.Index(name, "["); i != -1 {
			name = name[:i]
		}
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		deps = append(deps, dependency{Name: name, Version: ver})
	}
	return deps, nil
}

func parsePipfileLock(data []byte) ([]dependency, error) {
	var lf struct {
		Default map[string]struct {
			Version string `json:"version"`
		} `json:"default"`
		Develop map[string]struct {
			Version string `json:"version"`
		} `json:"develop"`
	}
	if err := json.Unmarshal(data, &lf); err != nil {
		return nil, fmt.Errorf("parsing Pipfile.lock: %w", err)
	}
	seen := map[string]bool{}
	var deps []dependency
	addPipfile := func(m map[string]struct {
		Version string `json:"version"`
	}) {
		for name, pkg := range m {
			if seen[name] {
				continue
			}
			seen[name] = true
			deps = append(deps, dependency{Name: name, Version: strings.TrimPrefix(pkg.Version, "==")})
		}
	}
	addPipfile(lf.Default)
	addPipfile(lf.Develop)
	return deps, nil
}

func parsePoetryLock(data []byte) ([]dependency, error) {
	seen := map[string]bool{}
	var deps []dependency
	var curName, curVer string
	inPkg := false
	for _, line := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(line)
		if t == "[[package]]" {
			if curName != "" && !seen[curName] {
				seen[curName] = true
				deps = append(deps, dependency{Name: curName, Version: curVer})
			}
			inPkg, curName, curVer = true, "", ""
			continue
		}
		if inPkg {
			if strings.HasPrefix(t, "name = ") {
				curName = strings.Trim(strings.TrimPrefix(t, "name = "), "\"")
			} else if strings.HasPrefix(t, "version = ") {
				curVer = strings.Trim(strings.TrimPrefix(t, "version = "), "\"")
			} else if strings.HasPrefix(t, "[") && t != "[[package]]" {
				inPkg = false
			}
		}
	}
	if curName != "" && !seen[curName] {
		deps = append(deps, dependency{Name: curName, Version: curVer})
	}
	return deps, nil
}

func parsePomXml(data []byte) ([]dependency, error) {
	type pomDep struct {
		GroupId    string `xml:"groupId"`
		ArtifactId string `xml:"artifactId"`
		Version    string `xml:"version"`
	}
	var pom struct {
		XMLName      xml.Name `xml:"project"`
		Dependencies struct {
			Dependency []pomDep `xml:"dependency"`
		} `xml:"dependencies"`
		DependencyManagement struct {
			Dependencies struct {
				Dependency []pomDep `xml:"dependency"`
			} `xml:"dependencies"`
		} `xml:"dependencyManagement"`
	}
	if err := xml.Unmarshal(data, &pom); err != nil {
		return nil, fmt.Errorf("parsing pom.xml: %w", err)
	}
	props := map[string]string{}
	propsRe := regexp.MustCompile(`<([a-zA-Z0-9._-]+)>([^<]+)</[a-zA-Z0-9._-]+>`)
	propsSection := regexp.MustCompile(`(?s)<properties>(.*?)</properties>`)
	if m := propsSection.FindSubmatch(data); len(m) > 1 {
		for _, pm := range propsRe.FindAllSubmatch(m[1], -1) {
			if len(pm) >= 3 {
				props[string(pm[1])] = string(pm[2])
			}
		}
	}
	resolveVer := func(v string) string {
		if strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}") {
			if r, ok := props[v[2:len(v)-1]]; ok {
				return r
			}
		}
		return v
	}
	seen := map[string]bool{}
	var deps []dependency
	all := append(pom.Dependencies.Dependency, pom.DependencyManagement.Dependencies.Dependency...)
	for _, d := range all {
		if d.GroupId == "" || d.ArtifactId == "" {
			continue
		}
		name := d.GroupId + ":" + d.ArtifactId
		ver := resolveVer(d.Version)
		if ver == "" {
			ver = "latest"
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		deps = append(deps, dependency{Name: name, Version: ver})
	}
	return deps, nil
}

func parseBuildGradle(data []byte) ([]dependency, error) {
	content := string(data)
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?:implementation|api|compile|runtimeOnly|testImplementation|testCompile|compileOnly)\s*\(\s*['"]([^'"]+):([^'"]+):([^'"]+)['"]\s*\)`),
		regexp.MustCompile(`(?:implementation|api|compile|runtimeOnly|testImplementation|testCompile|compileOnly)\s*\(\s*group:\s*['"]([^'"]+)['"]\s*,\s*name:\s*['"]([^'"]+)['"]\s*,\s*version:\s*['"]([^'"]+)['"]\s*\)`),
		regexp.MustCompile(`(?:implementation|api|compile|runtimeOnly|testImplementation|testCompile|compileOnly)\s+['"]([^'"]+):([^'"]+):([^'"]+)['"]`),
		regexp.MustCompile(`(?:implementation|api|compile|runtimeOnly|testImplementation|testCompile|compileOnly)\s*\(\s*"([^"]+):([^"]+):([^"]+)"\s*\)`),
	}
	seen := map[string]bool{}
	var deps []dependency
	for _, p := range patterns {
		for _, m := range p.FindAllStringSubmatch(content, -1) {
			if len(m) < 4 {
				continue
			}
			name := m[1] + ":" + m[2]
			if seen[name] {
				continue
			}
			seen[name] = true
			deps = append(deps, dependency{Name: name, Version: m[3]})
		}
	}
	return deps, nil
}
