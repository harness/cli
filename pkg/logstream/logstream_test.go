// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package logstream

import "testing"

func TestFormatLogLine(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string
	}{
		{
			name: "plain text envelope",
			line: `{"level":"info","time":"2026-09-07T08:14:53Z","out":"1.7.23: Pulling from gar-prod-setup/harness-public/harness/harness-cache-server"}`,
			want: "2026-09-07 08:14:53 [info] 1.7.23: Pulling from gar-prod-setup/harness-public/harness/harness-cache-server",
		},
		{
			name: "not a JSON envelope",
			line: "some raw non-JSON output",
			want: "some raw non-JSON output",
		},
		{
			name: "double-wrapped structured JSON log",
			line: `{"level":"info","time":"2026-09-07T08:14:54.842Z","out":"{\"severity\":\"INFO\",\"ts\":\"2026-09-07T08:14:54.842Z\",\"caller\":\"server/boot_log.go:10\",\"message\":\"running cache server in proxy mode\",\"application_name\":\"cache-service\",\"deployment\":\"cache-service\",\"environment\":\"dev\",\"self_hosted\":false,\"maven_max_blob_size_bytes\":5368709120,\"bind\":\":8082\"}"}`,
			want: "2026-09-07 08:14:54 [info] running cache server in proxy mode  application_name=cache-service bind=:8082 caller=server/boot_log.go:10 deployment=cache-service environment=dev maven_max_blob_size_bytes=5368709120 self_hosted=false",
		},
		{
			name: "double-wrapped log using msg field and no extra keys",
			line: `{"level":"debug","time":"2026-09-07T08:15:54Z","out":"{\"msg\":\"tick\"}"}`,
			want: "2026-09-07 08:15:54 [debug] tick",
		},
		{
			name: "out is JSON but not a recognizable structured log",
			line: `{"level":"info","time":"2026-09-07T08:15:54Z","out":"{\"foo\":\"bar\"}"}`,
			want: `2026-09-07 08:15:54 [info] {"foo":"bar"}`,
		},
		{
			name: "delegate timestamped metrics sample",
			line: `{"level":"info","time":"2026-09-07T08:14:37Z","out":"{\"2026-09-07T08:14:37.098151888Z\":{\"totalMemory\":30.356487274169922,\"totalCPU\":8,\"avaMemory\":29.541942596435547,\"avalCPU\":97.11417816813044,\"diskReadBytesSec\":0,\"diskWriteBytesSec\":0}}"}`,
			want: "2026-09-07 08:14:37 [perf] avaMemory=29.541942596435547, avalCPU=97.11417816813044, diskReadBytesSec=0, diskWriteBytesSec=0, totalCPU=8, totalMemory=30.356487274169922",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := formatLogLine(tc.line, false)
			if got != tc.want {
				t.Errorf("formatLogLine() =\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}

func TestUnwrapInnerJSON(t *testing.T) {
	msg, kvs, ok := unwrapInnerJSON(`{"severity":"INFO","message":"hello","environment":"dev","deployment":"cache-service"}`)
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if msg != "hello" {
		t.Errorf("msg = %q, want %q", msg, "hello")
	}
	want := []string{"deployment=cache-service", "environment=dev"}
	if len(kvs) != len(want) {
		t.Fatalf("kvs = %v, want %v", kvs, want)
	}
	for i := range want {
		if kvs[i] != want[i] {
			t.Errorf("kvs[%d] = %q, want %q", i, kvs[i], want[i])
		}
	}

	if _, _, ok := unwrapInnerJSON("not json"); ok {
		t.Errorf("expected ok=false for non-JSON input")
	}
	if _, _, ok := unwrapInnerJSON(`{"no_message_field":true}`); ok {
		t.Errorf("expected ok=false when no message/msg field present")
	}
}

func TestUnwrapTimestampedMetrics(t *testing.T) {
	ts, kvs, ok := unwrapTimestampedMetrics(`{"2026-09-07T08:14:37.098151888Z":{"totalCPU":8,"avaMemory":29.5}}`)
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if got := ts.UTC().Format("2006-01-02 15:04:05"); got != "2026-09-07 08:14:37" {
		t.Errorf("ts = %q, want %q", got, "2026-09-07 08:14:37")
	}
	want := []string{"avaMemory=29.5", "totalCPU=8"}
	if len(kvs) != len(want) {
		t.Fatalf("kvs = %v, want %v", kvs, want)
	}
	for i := range want {
		if kvs[i] != want[i] {
			t.Errorf("kvs[%d] = %q, want %q", i, kvs[i], want[i])
		}
	}

	if _, _, ok := unwrapTimestampedMetrics("not json"); ok {
		t.Errorf("expected ok=false for non-JSON input")
	}
	if _, _, ok := unwrapTimestampedMetrics(`{"not-a-timestamp":{"a":1}}`); ok {
		t.Errorf("expected ok=false when key isn't a timestamp")
	}
	if _, _, ok := unwrapTimestampedMetrics(`{"2026-09-07T08:14:37Z":"not an object"}`); ok {
		t.Errorf("expected ok=false when value isn't an object")
	}
	if _, _, ok := unwrapTimestampedMetrics(`{"2026-09-07T08:14:37Z":{},"other":1}`); ok {
		t.Errorf("expected ok=false when more than one top-level key")
	}
}
