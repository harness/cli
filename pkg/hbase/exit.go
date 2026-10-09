// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package hbase

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/harness/cli/v3/pkg/hlog"
)

var exitHooks struct {
	sync.Mutex
	hooks []func() error
}

func RegisterExitHook(hook func() error) {
	exitHooks.Lock()
	defer exitHooks.Unlock()
	exitHooks.hooks = append(exitHooks.hooks, hook)
}

func Cleanup() error {
	exitHooks.Lock()
	hooks := exitHooks.hooks
	exitHooks.hooks = nil
	exitHooks.Unlock()
	var err error
	for _, hook := range hooks {
		if hookErr := runExitHook(hook); hookErr != nil {
			hlog.Debug("exit cleanup failed", "err", hookErr)
			err = errors.Join(err, hookErr)
		}
	}
	return err
}

func runExitHook(hook func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("exit hook panicked: %v", r)
		}
	}()
	return hook()
}

func Exit(code int) {
	_ = Cleanup()
	os.Exit(code)
}
