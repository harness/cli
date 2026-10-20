// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package httpclient

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"

	"github.com/harness/cli/v3/pkg/hlog"
)

// DoWithProxyCheck fails on an unparseable proxy env var, which http.ProxyFromEnvironment would silently ignore.
func DoWithProxyCheck(hc *http.Client, req *http.Request) (*http.Response, error) {
	if err := checkProxyEnv(req.URL); err != nil {
		return nil, err
	}
	return hc.Do(req)
}

func checkProxyEnv(u *url.URL) error {
	host := u.Hostname()
	if ip := net.ParseIP(host); host == "localhost" || (ip != nil && ip.IsLoopback()) {
		return nil
	}
	names := []string{"HTTP_PROXY", "http_proxy"}
	if u.Scheme == "https" {
		names = []string{"HTTPS_PROXY", "https_proxy"}
	}
	for _, name := range names {
		value := os.Getenv(name)
		if value == "" {
			continue
		}
		p, err := url.Parse(value)
		if err != nil || p.Scheme == "" || p.Host == "" {
			p, err = url.Parse("http://" + value)
		}
		if err != nil {
			return fmt.Errorf("%s is not a valid proxy URL: it contains invalid characters, such as an unescaped '%%'. Go would silently ignore it and connect directly. URL-encode special characters in the proxy username/password (e.g. '%%' as '%%25', '@' as '%%40') and retry", name)
		}
		hlog.Debug("using proxy from environment", "var", name, "host", p.Host)
		return nil
	}
	return nil
}
