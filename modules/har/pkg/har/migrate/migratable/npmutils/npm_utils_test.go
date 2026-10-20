// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package npmutils

import (
	"io"
	"strings"
	"testing"
)

func TestWithPackageNameKeepsOtherFields(t *testing.T) {
	in := []byte(`{"name":"@old/telemetry","version":"1.0.6","dependencies":{"lodash":"^4.17.21"}}`)

	out, err := WithPackageName(in, "@new/telemetry")
	if err != nil {
		t.Fatalf("WithPackageName: %v", err)
	}
	upload, name, version, err := BuildNpmUploadFromPackageJSON(out, "", io.NopCloser(strings.NewReader("tgz")))
	if err != nil {
		t.Fatalf("BuildNpmUploadFromPackageJSON: %v", err)
	}
	if name != "@new/telemetry" || version != "1.0.6" {
		t.Errorf("got %s@%s, want @new/telemetry@1.0.6", name, version)
	}
	deps, _ := upload.Versions["1.0.6"].Dependencies.(map[string]interface{})
	if deps["lodash"] != "^4.17.21" {
		t.Errorf("dependencies = %v, want lodash preserved", upload.Versions["1.0.6"].Dependencies)
	}
}
