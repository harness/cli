// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"testing"

	"go.yaml.in/yaml/v3"
)

const inputTemplate = `
pipeline:
  identifier: p
  variables:
    - name: image_tag
      type: String
      value: <+input>
  stages:
    - stage:
        identifier: build
        variables:
          - name: image_tag
            type: String
            value: <+input>
          - name: region
            type: String
            value: eu
    - stage:
        identifier: deploy
        variables:
          - name: image_tag
            type: String
            value: <+input>
`

// imageTags returns the image_tag value at pipeline level, in build and in deploy.
func imageTags(t *testing.T, out string) [3]string {
	t.Helper()
	var doc struct {
		Pipeline struct {
			Variables []struct{ Value string }
			Stages    []struct {
				Stage struct {
					Variables []struct{ Value string }
				}
			}
		}
	}
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("parsing merged yaml: %v", err)
	}
	p := doc.Pipeline
	return [3]string{p.Variables[0].Value, p.Stages[0].Stage.Variables[0].Value, p.Stages[1].Stage.Variables[0].Value}
}

func TestMergeIntoTemplate(t *testing.T) {
	tests := []struct {
		name   string
		inputs map[string]string
		want   [3]string
	}{
		{"unscoped key replaces every occurrence", map[string]string{"image_tag": "v2"}, [3]string{"v2", "v2", "v2"}},
		{"key match is case-insensitive", map[string]string{"IMAGE_TAG": "v2"}, [3]string{"v2", "v2", "v2"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := mergeIntoTemplate(inputTemplate, tc.inputs)
			if err != nil {
				t.Fatalf("mergeIntoTemplate: %v", err)
			}
			if got := imageTags(t, out); got != tc.want {
				t.Errorf("image_tag values = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMergeIntoTemplate_UnmatchedKeys(t *testing.T) {
	tests := []struct {
		name    string
		inputs  map[string]string
		wantErr string
	}{
		{"typo in key", map[string]string{"image_tga": "v2"}, `--input "image_tga" matched no runtime input`},
		{"key is a fixed value", map[string]string{"region": "us"}, `--input "region" is not a runtime input in this pipeline`},
		{"one bad key among good ones", map[string]string{"image_tag": "v2", "nope": "x"}, `--input "nope" matched no runtime input`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := mergeIntoTemplate(inputTemplate, tc.inputs)
			if err == nil || err.Error() != tc.wantErr {
				t.Errorf("error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}
