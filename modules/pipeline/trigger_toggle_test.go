// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"strings"
	"testing"
)

const triggerDoc = `# keep me
trigger:
  name: My Trigger
  identifier: my_trigger
  enabled: true # toggled by the CLI
  pipelineIdentifier: my_pipeline
  source:
    type: Webhook
    spec:
      type: Custom
      spec:
        payloadConditions: []
`

func TestSetTriggerEnabled(t *testing.T) {
	t.Run("flips_value_and_preserves_order_and_comments", func(t *testing.T) {
		got, changed, err := setTriggerEnabled(triggerDoc, false)
		if err != nil || !changed {
			t.Fatalf("changed=%v err=%v, want changed and no error", changed, err)
		}
		want := strings.Replace(triggerDoc, "enabled: true", "enabled: false", 1)
		if got != want {
			t.Fatalf("output differs beyond the enabled value:\n%s", got)
		}
	})

	t.Run("already_in_state_is_unchanged", func(t *testing.T) {
		got, changed, err := setTriggerEnabled(triggerDoc, true)
		if err != nil || changed || got != triggerDoc {
			t.Fatalf("changed=%v err=%v, want untouched document", changed, err)
		}
	})

	t.Run("quoted_string_is_rewritten_as_bool", func(t *testing.T) {
		doc := strings.Replace(triggerDoc, "enabled: true", `enabled: "true"`, 1)
		got, changed, err := setTriggerEnabled(doc, true)
		if err != nil || !changed || !strings.Contains(got, "enabled: true # toggled") {
			t.Fatalf("changed=%v err=%v got:\n%s", changed, err, got)
		}
	})

	for _, tc := range []struct{ name, doc, wantErr string }{
		{"not_a_mapping", "- a\n- b\n", "not a mapping"},
		{"no_trigger_key", "name: x\n", `no "trigger" mapping`},
		{"no_enabled_key", "trigger:\n  name: x\n", `"trigger.enabled"`},
		{"invalid_yaml", "trigger: [", "parsing trigger YAML"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := setTriggerEnabled(tc.doc, false)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}
