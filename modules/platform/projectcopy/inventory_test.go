// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package projectcopy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

const marker = "SYNTHETIC_SENSITIVE_MARKER"

var testScope = Scope{Account: "fixture_account", Org: "fixture_org", Source: "source", Destination: "destination"}

func fixture(id string) record {
	updated := int64(17)
	return record{Secret: &secret{Identifier: id, Name: marker, Org: testScope.Org, Project: testScope.Source, Type: "SecretText", Spec: map[string]json.RawMessage{"type": json.RawMessage(`"SecretText"`), "valueType": json.RawMessage(`"Reference"`), "secretManagerIdentifier": json.RawMessage(`"org.manager"`), "value": json.RawMessage(`"` + marker + `"`)}}, Updated: &updated}
}

func fixtureReader(count int, mutate func(int, *pageData)) readFn {
	records := make([]record, count)
	for i := range records {
		records[i] = fixture(fmt.Sprintf("secret_%03d", i))
	}
	return func(_ context.Context, _ Scope, id string, index int, target any) error {
		var value any
		if id != "" {
			for _, r := range records {
				if r.Secret.Identifier == id {
					value = map[string]any{"status": "SUCCESS", "data": r}
				}
			}
		} else {
			pages, total, size := (count+99)/100, count, 100
			start := min(index*100, count)
			end := min(start+100, count)
			n := end - start
			p := pageData{TotalPages: &pages, TotalItems: &total, PageSize: &size, PageItemCount: &n, Content: append([]record{}, records[start:end]...)}
			if mutate != nil {
				mutate(index, &p)
			}
			value = map[string]any{"status": "SUCCESS", "data": p}
		}
		data, _ := json.Marshal(value)
		return json.Unmarshal(data, target)
	}
}

func TestInventoryCompleteAndPayloadSafe(t *testing.T) {
	for _, count := range []int{0, 1, 100, 101, 201} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			r, err := inventory(context.Background(), fixtureReader(count, nil), testScope)
			if err != nil || !r.InventoryComplete || r.ApplyAvailable || len(r.Secrets) != count {
				t.Fatalf("count=%d complete=%v err=%v", len(r.Secrets), r.InventoryComplete, err)
			}
			data, _ := json.Marshal(r)
			for _, s := range []string{marker, "org.manager", "secret_000", "valueType"} {
				if bytes.Contains(data, []byte(s)) {
					t.Fatal("unsafe API content in report")
				}
			}
			for _, item := range r.Secrets {
				if item.Capability != ReferenceOnly {
					t.Fatal("unexpected capability")
				}
			}
		})
	}
}

func TestInventoryRejectsIncompletePages(t *testing.T) {
	cases := map[string]func(int, *pageData){
		"missing total":   func(_ int, p *pageData) { p.TotalItems = nil },
		"missing content": func(_ int, p *pageData) { p.Content = nil },
		"missing pages":   func(_ int, p *pageData) { p.TotalPages = nil },
		"missing count":   func(_ int, p *pageData) { p.PageItemCount = nil },
		"truncated":       func(_ int, p *pageData) { p.Content = p.Content[:len(p.Content)-1] },
		"wrong scope":     func(_ int, p *pageData) { p.Content[0].Secret.Project = "other" },
		"wrong org":       func(_ int, p *pageData) { p.Content[0].Secret.Org = "other" },
		"duplicate":       func(_ int, p *pageData) { p.Content[1] = p.Content[0] },
		"changed total": func(i int, p *pageData) {
			if i > 0 {
				*p.TotalItems = 102
			}
		},
		"wrong page size":  func(_ int, p *pageData) { *p.PageSize = 50 },
		"draft":            func(_ int, p *pageData) { p.Content[0].Draft = true },
		"missing revision": func(_ int, p *pageData) { p.Content[0].Updated = nil },
		"wrong pages":      func(_ int, p *pageData) { *p.TotalPages = 9 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r, err := inventory(context.Background(), fixtureReader(101, mutate), testScope)
			if err == nil || r.InventoryComplete {
				t.Fatal("invalid inventory accepted")
			}
		})
	}
}

func TestCompletedInventoryStillFailsForRequiredMappings(t *testing.T) {
	for _, kind := range []string{"Inline", "Unknown"} {
		base := fixtureReader(1, nil)
		read := func(ctx context.Context, s Scope, id string, page int, target any) error {
			if id == "" {
				return base(ctx, s, id, page, target)
			}
			r := fixture(id)
			r.Secret.Spec["valueType"] = json.RawMessage(`"` + kind + `"`)
			data, _ := json.Marshal(map[string]any{"status": "SUCCESS", "data": r})
			return json.Unmarshal(data, target)
		}
		r, err := inventory(context.Background(), read, testScope)
		if err == nil || !r.InventoryComplete || r.ApplyAvailable || len(r.Secrets) != 1 {
			t.Fatal("unsupported plan appeared usable")
		}
	}
}

func TestInventoryDetailFailuresAndChurn(t *testing.T) {
	for _, mode := range []string{"denied", "missing", "scope", "identity", "type", "revision", "churn", "status"} {
		t.Run(mode, func(t *testing.T) {
			base := fixtureReader(1, nil)
			lists := 0
			read := func(ctx context.Context, s Scope, id string, page int, target any) error {
				if id == "" {
					lists++
					if lists == 2 && mode == "churn" {
						return fixtureReader(0, nil)(ctx, s, id, page, target)
					}
					return base(ctx, s, id, page, target)
				}
				if mode == "denied" {
					return errors.New("safe denied")
				}
				r := fixture(id)
				status := "SUCCESS"
				switch mode {
				case "missing":
					r.Secret = nil
				case "scope":
					r.Secret.Project = "other"
				case "identity":
					r.Secret.Identifier = "other"
				case "type":
					r.Secret.Type = "SecretFile"
				case "revision":
					*r.Updated++
				case "status":
					status = "FAILURE"
				}
				data, _ := json.Marshal(map[string]any{"status": status, "data": r})
				return json.Unmarshal(data, target)
			}
			r, err := inventory(context.Background(), read, testScope)
			if err == nil || r.InventoryComplete {
				t.Fatal("detail inconsistency accepted")
			}
		})
	}
}

func TestSecretCapabilities(t *testing.T) {
	for _, tc := range []struct {
		name   string
		modify func(*secret)
		want   Capability
	}{
		{"reference", func(*secret) {}, ReferenceOnly},
		{"live inline", func(s *secret) {
			s.Spec = map[string]json.RawMessage{"secretManagerIdentifier": json.RawMessage(`"harnessSecretManager"`), "valueType": json.RawMessage(`"Inline"`), "value": json.RawMessage(`null`), "additionalMetadata": json.RawMessage(`null`)}
		}, MappingRequired},
		{"inline", func(s *secret) { s.Spec["valueType"] = json.RawMessage(`"Inline"`); delete(s.Spec, "value") }, MappingRequired},
		{"unknown value type", func(s *secret) { s.Spec["valueType"] = json.RawMessage(`"Unknown"`) }, Blocked},
		{"unknown field", func(s *secret) { s.Spec["credentials"] = json.RawMessage(`"` + marker + `"`) }, Blocked},
		{"null value", func(s *secret) { s.Spec["value"] = json.RawMessage(`null`) }, Blocked},
		{"missing locator", func(s *secret) { delete(s.Spec, "value") }, Blocked},
		{"missing manager", func(s *secret) { delete(s.Spec, "secretManagerIdentifier") }, Blocked},
		{"mismatched spec", func(s *secret) { s.Spec["type"] = json.RawMessage(`"SSHKey"`) }, Blocked},
		{"unknown type", func(s *secret) { s.Type = marker }, Blocked},
		{"file", func(s *secret) { s.Type = "SecretFile"; s.Spec["type"] = json.RawMessage(`"SecretFile"`) }, Blocked},
		{"default manager", func(s *secret) { s.Spec["secretManagerIdentifier"] = json.RawMessage(`"harnessSecretManager"`) }, Blocked},
		{"scoped default manager", func(s *secret) { s.Spec["secretManagerIdentifier"] = json.RawMessage(`"account.harnessSecretManager"`) }, Blocked},
		{"dynamic manager", func(s *secret) { s.Spec["secretManagerIdentifier"] = json.RawMessage(`"<+input>"`) }, Blocked},
		{"dynamic locator", func(s *secret) { s.Spec["value"] = json.RawMessage(`"<+input>"`) }, Blocked},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := fixture("sample").Secret
			tc.modify(s)
			got := classify(s)
			if got.Capability != tc.want {
				t.Fatalf("got %s want %s", got.Capability, tc.want)
			}
			data, _ := json.Marshal(got)
			if bytes.Contains(data, []byte(marker)) {
				t.Fatal("unsafe capability output")
			}
		})
	}
}

func TestUnknownSecretRootFieldsBlock(t *testing.T) {
	var s secret
	if err := json.Unmarshal([]byte(`{"type":"SecretText","privateSecret":true}`), &s); err != nil {
		t.Fatal(err)
	}
	if !s.Unproven || classify(&s).Capability != Blocked {
		t.Fatal("unknown root field accepted")
	}
}

func rewriteReader(base readFn, rewrite func(string, int, []byte) []byte) readFn {
	return func(ctx context.Context, s Scope, id string, page int, target any) error {
		var value any
		if err := base(ctx, s, id, page, &value); err != nil {
			return err
		}
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		return json.Unmarshal(rewrite(id, page, data), target)
	}
}

func TestInventoryRejectsAmbiguousControls(t *testing.T) {
	for _, tc := range []struct{ name, from, to string }{
		{"status aliases", `"status":"SUCCESS"`, `"status":"ERROR","Status":"SUCCESS"`},
		{"status aliases reversed", `"status":"SUCCESS"`, `"Status":"SUCCESS","status":"ERROR"`},
		{"status alias only", `"status":"SUCCESS"`, `"Status":"SUCCESS"`},
		{"null status", `"status":"SUCCESS"`, `"status":null`},
		{"draft aliases", `"draft":false`, `"draft":true,"Draft":false`},
		{"draft aliases reversed", `"draft":false`, `"Draft":false,"draft":true`},
		{"draft alias only", `"draft":false`, `"Draft":false`},
		{"null draft", `"draft":false`, `"draft":null`},
		{"string draft", `"draft":false`, `"draft":"false"`},
		{"data aliases", `"data":`, `"Data":null,"data":`},
		{"revision aliases", `"updatedAt":17`, `"updatedAt":-1,"UpdatedAt":17`},
		{"scope aliases", `"projectIdentifier":"source"`, `"projectIdentifier":"other","ProjectIdentifier":"source"`},
	} {
		for _, detail := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/detail=%v", tc.name, detail), func(t *testing.T) {
				calls := 0
				read := rewriteReader(fixtureReader(1, nil), func(id string, _ int, data []byte) []byte {
					calls++
					if (id != "") == detail {
						return []byte(strings.Replace(string(data), tc.from, tc.to, 1))
					}
					return data
				})
				r, err := inventory(context.Background(), read, testScope)
				wantCalls := 1
				if detail {
					wantCalls = 2
				}
				if err == nil || r.InventoryComplete || len(r.Secrets) != 0 || calls != wantCalls {
					t.Fatalf("ambiguous controls accepted: calls=%d complete=%v err=%v", calls, r.InventoryComplete, err)
				}
			})
		}
	}
}

func TestInventoryValidatesOptionalPaginationControls(t *testing.T) {
	for _, control := range []string{
		`"pageIndex":99`, `"pageIndex":null`, `"pageIndex":"0"`, `"PageIndex":0`,
		`"empty":true`, `"empty":null`, `"empty":0`,
		`"pageToken":{}`, `"pageToken":null`, `"pageToken":"unproven_continuation"`,
		`"nextCursor":"unproven_continuation"`,
	} {
		t.Run(control, func(t *testing.T) {
			read := rewriteReader(fixtureReader(1, nil), func(id string, _ int, data []byte) []byte {
				if id == "" {
					return []byte(strings.Replace(string(data), `"pageSize":100`, `"pageSize":100,`+control, 1))
				}
				return data
			})
			r, err := inventory(context.Background(), read, testScope)
			if err == nil || r.InventoryComplete {
				t.Fatal("unproven pagination accepted")
			}
		})
	}
	for _, tc := range []struct {
		name  string
		count int
	}{
		{"wrong later page", 101}, {"false empty page", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			read := fixtureReader(tc.count, func(_ int, p *pageData) {
				zero, empty := 0, false
				p.PageIndex, p.Empty = &zero, &empty
			})
			r, err := inventory(context.Background(), read, testScope)
			if err == nil || r.InventoryComplete {
				t.Fatal("inconsistent page controls accepted")
			}
		})
	}
}

func TestInventoryPreservesDocumentedPaginationAndOptionalDraft(t *testing.T) {
	for _, tc := range []struct {
		count      int
		legacyZero bool
	}{
		{0, false}, {0, true}, {201, false},
	} {
		t.Run(fmt.Sprintf("count=%d/legacyZero=%v", tc.count, tc.legacyZero), func(t *testing.T) {
			base := fixtureReader(tc.count, func(index int, p *pageData) {
				empty, token := len(p.Content) == 0, ""
				p.PageIndex, p.Empty, p.PageToken = &index, &empty, &token
				if tc.legacyZero {
					*p.TotalPages = 1
				}
			})
			read := rewriteReader(base, func(_ string, _ int, data []byte) []byte {
				body := strings.ReplaceAll(string(data), `"draft":false,`, "")
				return []byte(strings.ReplaceAll(body, `"name":`, `"tags":{"Owner":"one","owner":"two"},"name":`))
			})
			r, err := inventory(context.Background(), read, testScope)
			if err != nil || !r.InventoryComplete || len(r.Secrets) != tc.count {
				t.Fatalf("documented pagination rejected: complete=%v err=%v", r.InventoryComplete, err)
			}
		})
	}
}

func TestInventoryDoesNotEraseUnknownListSchema(t *testing.T) {
	for _, tc := range []struct {
		name   string
		count  int
		page   int
		relist bool
	}{
		{"first page", 1, 0, false}, {"later page", 101, 1, false}, {"relist", 1, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lists := 0
			read := rewriteReader(fixtureReader(tc.count, nil), func(id string, page int, data []byte) []byte {
				if id == "" {
					lists++
					isRelist := lists > (tc.count+99)/100
					if page == tc.page && isRelist == tc.relist {
						return []byte(strings.Replace(string(data), `"spec":`, `"unprovenControl":true,"spec":`, 1))
					}
				}
				return data
			})
			r, err := inventory(context.Background(), read, testScope)
			if err == nil || r.InventoryComplete {
				t.Fatal("unknown list schema erased by minimal detail")
			}
		})
	}
}
