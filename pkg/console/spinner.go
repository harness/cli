// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package console

import (
	"sync/atomic"
	"time"

	"github.com/pterm/pterm"
)

const stageHeartbeatInterval = 15 * time.Second

var spinnerBusy atomic.Bool

// StageProgress reports liveness for a long-running operation.
type StageProgress struct {
	text      string
	startedAt time.Time
	spinner   *pterm.SpinnerPrinter
	stop      chan struct{}
	done      chan struct{}
}

// StartStage starts an animated spinner on interactive terminals and heartbeat
// messages when stdout is redirected.
func StartStage(text string) *StageProgress {
	s := &StageProgress{text: text, startedAt: time.Now()}

	if IsStdoutTTY() && spinnerBusy.CompareAndSwap(false, true) {
		spinner, err := pterm.DefaultSpinner.
			WithRemoveWhenDone(true).
			WithShowTimer(true).
			Start(text)
		if err == nil {
			s.spinner = spinner
			return s
		}
		spinnerBusy.Store(false)
	}

	pterm.Info.Println(text)
	s.stop = make(chan struct{})
	s.done = make(chan struct{})
	go s.heartbeat()
	return s
}

// Success ends the stage with a success line carrying the elapsed time.
func (s *StageProgress) Success(msg string) {
	s.halt()
	pterm.Success.Println(s.withElapsed(msg))
}

// Warn ends the stage with a warning line.
func (s *StageProgress) Warn(msg string) {
	s.halt()
	pterm.Warning.Println(s.withElapsed(msg))
}

// Fail ends the stage with an error line.
func (s *StageProgress) Fail(msg string) {
	s.halt()
	pterm.Error.Println(s.withElapsed(msg))
}

func (s *StageProgress) heartbeat() {
	defer close(s.done)

	ticker := time.NewTicker(stageHeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			pterm.Info.Println(s.withElapsed(s.text + " — still working"))
		}
	}
}

// halt stops the animation or heartbeat. Safe to call more than once.
func (s *StageProgress) halt() {
	if s.spinner != nil {
		_ = s.spinner.Stop()
		s.spinner = nil
		spinnerBusy.Store(false)
		return
	}
	if s.stop != nil {
		close(s.stop)
		<-s.done
		s.stop = nil
	}
}

func (s *StageProgress) withElapsed(msg string) string {
	return msg + " (" + time.Since(s.startedAt).Round(time.Second).String() + ")"
}
