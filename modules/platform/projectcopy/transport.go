// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package projectcopy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/harness/cli/v3/pkg/auth"
)

const secretsPath = "/ng/api/v2/secrets"
const responseLimit = 4 << 20

type Transport struct {
	auth *auth.ResolvedAuth
	http *http.Client
	wait func(context.Context, time.Duration) error
}

func NewTransport(a *auth.ResolvedAuth) *Transport {
	return &Transport{auth: a, http: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, wait: wait}
}

func wait(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return errors.New("secret inventory cancelled")
	}
}

// exchange performs one scoped API call. Response bodies are returned to the caller and never logged.
// HTTP statuses are returned as-is so callers can tell a missing resource from a transport failure.
func (t *Transport) exchange(ctx context.Context, method, path string, query url.Values, body, contentType string) (int, []byte, error) {
	base, err := url.Parse(t.auth.APIUrl)
	if err != nil || base.Scheme != "https" || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return 0, nil, errors.New("project copy requires a valid HTTPS API base URL without credentials, query or fragment")
	}
	base.Path = strings.TrimRight(base.Path, "/") + path
	if query != nil {
		base.RawQuery = query.Encode()
	}
	attempts := retryLimit(method, path, body)
	for attempt := 0; attempt < attempts; attempt++ {
		if err := refreshAuth(t.auth); err != nil {
			return 0, nil, err
		}
		var reader io.Reader
		if body != "" {
			reader = strings.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, base.String(), reader)
		if err != nil {
			return 0, nil, errors.New("cannot prepare project copy request")
		}
		t.auth.SetAuthHeader(req)
		req.Header.Set("Accept", "application/json")
		if body != "" {
			if contentType == "" {
				contentType = "application/json"
			}
			req.Header.Set("Content-Type", contentType)
		}
		resp, err := t.http.Do(req)
		if err != nil {
			if attempt == attempts-1 || ctx.Err() != nil {
				return 0, nil, errors.New("project copy request failed")
			}
			if t.wait(ctx, time.Duration(attempt+1)*time.Second) != nil {
				return 0, nil, errors.New("project copy cancelled")
			}
			continue
		}
		retry := resp.StatusCode == 429 || resp.StatusCode == 502 || resp.StatusCode == 503 || resp.StatusCode == 504
		if retry && attempt < attempts-1 {
			delay := time.Duration(attempt+1) * time.Second
			if raw := resp.Header.Get("Retry-After"); raw != "" {
				if seconds, e := strconv.Atoi(raw); e == nil && seconds >= 0 {
					delay = time.Duration(min(seconds, 11)) * time.Second
				} else if date, e := http.ParseTime(raw); e == nil {
					delay = max(time.Until(date), 0)
				} else {
					resp.Body.Close()
					return 0, nil, errors.New("project copy retry policy unavailable")
				}
			}
			resp.Body.Close()
			if delay > 10*time.Second {
				return 0, nil, errors.New("project copy rate limited; retry later")
			}
			if t.wait(ctx, delay) != nil {
				return 0, nil, errors.New("project copy cancelled")
			}
			continue
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, responseLimit+1))
		resp.Body.Close()
		if err != nil || len(data) > responseLimit {
			return 0, nil, errors.New("project copy response incomplete or too large")
		}
		if len(data) > 0 && (!json.Valid(data) || !unambiguousJSON(data)) {
			return 0, nil, errors.New("malformed project copy response")
		}
		return resp.StatusCode, data, nil
	}
	return 0, nil, errors.New("project copy retries exhausted")
}

// The POST below is a documented list operation, never a resource creation.
func (t *Transport) read(ctx context.Context, scope Scope, id string, page int, target any) error {
	base, err := url.Parse(t.auth.APIUrl)
	if err != nil || base.Scheme != "https" || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return errors.New("secret inventory requires a valid HTTPS API base URL without credentials, query or fragment")
	}
	method, path, body := http.MethodPost, secretsPath+"/list/secrets", `{"filterType":"Secret"}`
	if id != "" {
		if !ValidIdentifier(id) {
			return errors.New("invalid secret inventory identity")
		}
		method, path, body = http.MethodGet, secretsPath+"/"+id, ""
	}
	base.Path = strings.TrimRight(base.Path, "/") + path
	q := url.Values{"accountIdentifier": {scope.Account}, "orgIdentifier": {scope.Org}, "projectIdentifier": {scope.Source}}
	if id == "" {
		q.Set("pageIndex", strconv.Itoa(page))
		q.Set("pageSize", "100")
	}
	base.RawQuery = q.Encode()
	for attempt := 0; attempt < 3; attempt++ {
		if err := refreshAuth(t.auth); err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, method, base.String(), strings.NewReader(body))
		if err != nil {
			return errors.New("cannot prepare secret inventory request")
		}
		t.auth.SetAuthHeader(req)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/json")
		// Do not use the generic client: even its raw path logs URLs and network errors.
		resp, err := t.http.Do(req)
		if err != nil {
			if attempt == 2 || ctx.Err() != nil {
				return errors.New("secret inventory request failed")
			}
			if t.wait(ctx, time.Duration(attempt+1)*time.Second) != nil {
				return errors.New("secret inventory cancelled")
			}
			continue
		}
		retry := resp.StatusCode == 429 || resp.StatusCode == 502 || resp.StatusCode == 503 || resp.StatusCode == 504
		if retry && attempt < 2 {
			delay := time.Duration(attempt+1) * time.Second
			if raw := resp.Header.Get("Retry-After"); raw != "" {
				if seconds, e := strconv.Atoi(raw); e == nil && seconds >= 0 {
					delay = time.Duration(min(seconds, 11)) * time.Second
				} else if date, e := http.ParseTime(raw); e == nil {
					delay = max(time.Until(date), 0)
				} else {
					resp.Body.Close()
					return errors.New("secret inventory retry policy unavailable")
				}
			}
			resp.Body.Close()
			if delay > 10*time.Second {
				return errors.New("secret inventory rate limited; retry later")
			}
			if t.wait(ctx, delay) != nil {
				return errors.New("secret inventory cancelled")
			}
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return errors.New("secret inventory read denied or unavailable")
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, responseLimit+1))
		resp.Body.Close()
		if err != nil || len(data) > responseLimit {
			return errors.New("secret inventory response incomplete or too large")
		}
		if !json.Valid(data) || !unambiguousJSON(data) || json.Unmarshal(data, target) != nil {
			return errors.New("malformed secret inventory response")
		}
		return nil
	}
	return errors.New("secret inventory retries exhausted")
}

func refreshAuth(a *auth.ResolvedAuth) error {
	if a == nil {
		return errors.New("project copy authentication required")
	}
	if err := auth.CheckAndUpdateAccessToken(a, time.Now()); err != nil {
		return errors.New("project copy authentication expired")
	}
	return nil
}

func retryLimit(method, path, body string) int {
	if method == http.MethodGet || method == http.MethodHead {
		return 3
	}
	if method == http.MethodPost && isReadPost(path, body) {
		return 3
	}
	return 1
}

func isReadPost(path, body string) bool {
	if strings.Contains(path, "/list") || strings.HasSuffix(path, "/listDelegates") {
		return true
	}
	return strings.Contains(body, `"filterType"`)
}

func canonicalObject(data []byte, controls ...string) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if !unambiguousJSON(data) || json.Unmarshal(data, &fields) != nil || fields == nil {
		return nil, errors.New("malformed secret inventory object")
	}
	for key, value := range fields {
		for _, control := range controls {
			if strings.EqualFold(key, control) && (key != control || string(value) == "null") {
				return nil, errors.New("ambiguous or null secret inventory control")
			}
		}
	}
	return fields, nil
}

func unambiguousJSON(data []byte) bool {
	dec := json.NewDecoder(strings.NewReader(string(data)))
	var value func() bool
	value = func() bool {
		token, err := dec.Token()
		if err != nil {
			return false
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return true
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for dec.More() {
				key, e := dec.Token()
				if e != nil {
					return false
				}
				s, ok := key.(string)
				if !ok || seen[s] {
					return false
				}
				seen[s] = true
				if !value() {
					return false
				}
			}
		case '[':
			for dec.More() {
				if !value() {
					return false
				}
			}
		default:
			return false
		}
		end, e := dec.Token()
		return e == nil && ((delim == '{' && end == json.Delim('}')) || (delim == '[' && end == json.Delim(']')))
	}
	if !value() {
		return false
	}
	_, err := dec.Token()
	return err == io.EOF
}
