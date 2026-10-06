// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build !windows

package har

import (
	"context"
	"syscall"
	"testing"
	"time"

	"github.com/harness/cli/v3/pkg/cmdctx"
)

func TestCancelOnSignalCancelsContext(t *testing.T) {
	goCtx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	ctx := &cmdctx.Ctx{Context: goCtx, CancelFn: cancel}

	stop := cancelOnSignal(ctx)
	defer stop()

	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("sending SIGTERM: %v", err)
	}

	select {
	case <-goCtx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("context was not cancelled after SIGTERM")
	}
	if got := context.Cause(goCtx).Error(); got != "push interrupted by terminated" {
		t.Fatalf("cause = %q, want %q", got, "push interrupted by terminated")
	}
}

func TestCancelOnSignalStopLeavesContextAlone(t *testing.T) {
	goCtx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	ctx := &cmdctx.Ctx{Context: goCtx, CancelFn: cancel}

	cancelOnSignal(ctx)()

	if goCtx.Err() != nil {
		t.Fatalf("context cancelled without a signal: %v", context.Cause(goCtx))
	}
}
