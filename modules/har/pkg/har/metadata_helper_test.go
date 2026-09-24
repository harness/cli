// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"reflect"
	"testing"
)

func TestParsePushMetadataString(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []map[string]string
		wantErr bool
	}{
		{
			name:  "empty string",
			input: "",
			want:  nil,
		},
		{
			name:  "single pair",
			input: "key:value",
			want:  []map[string]string{{"key": "key", "value": "value"}},
		},
		{
			name:  "multiple pairs",
			input: "key:value,key2:value2",
			want: []map[string]string{
				{"key": "key", "value": "value"},
				{"key": "key2", "value": "value2"},
			},
		},
		{
			name:  "whitespace trimmed",
			input: " key : value , key2 : value2 ",
			want: []map[string]string{
				{"key": "key", "value": "value"},
				{"key": "key2", "value": "value2"},
			},
		},
		{
			name:  "value containing colon",
			input: "url:https://example.com",
			want:  []map[string]string{{"key": "url", "value": "https://example.com"}},
		},
		{
			name:    "missing colon",
			input:   "keyvalue",
			wantErr: true,
		},
		{
			name:    "empty key",
			input:   ":value",
			wantErr: true,
		},
		{
			name:    "empty value",
			input:   "key:",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parsePushMetadataString(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
