// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package console

import (
	"fmt"
	"sync/atomic"
	"time"
)

const stageHeartbeatInterval = 15 * time.Second

var spinnerBusy atomic.Bool

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const spinnerFrameInterval = 100 * time.Millisecond

// StageProgress reports liveness for a long-running operation.
type StageProgress struct {
	text      string
	startedAt time.Time
	animating bool
	stop      chan struct{}
	done      chan struct{}
}

// StartStage starts an animated spinner on interactive terminals and heartbeat
// messages when stdout is redirected.
func StartStage(text string) *StageProgress {
	s := &StageProgress{
		text:      text,
		startedAt: time.Now(),
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}

	if IsStdoutTTY() && spinnerBusy.CompareAndSwap(false, true) {
		s.animating = true
		go s.animate()
		return s
	}

	fmt.Println(WithColor(ColorCyan, "→") + " " + text)
	go s.heartbeat()
	return s
}

// Success ends the stage with a success line carrying the elapsed time.
func (s *StageProgress) Success(msg string) {
	s.halt()
	fmt.Println(GreenCheck() + " " + s.withElapsed(msg))
}

// Warn ends the stage with a warning line.
func (s *StageProgress) Warn(msg string) {
	s.halt()
	fmt.Println(YellowWarning() + " " + s.withElapsed(msg))
}

// Fail ends the stage with an error line.
func (s *StageProgress) Fail(msg string) {
	s.halt()
	fmt.Println(RedX() + " " + s.withElapsed(msg))
}

func (s *StageProgress) animate() {
	defer close(s.done)

	fmt.Print("\x1b[?25l")
	ticker := time.NewTicker(spinnerFrameInterval)
	defer ticker.Stop()

	i := 0
	for {
		select {
		case <-s.stop:
			fmt.Print("\r\x1b[K\x1b[?25h")
			return
		case <-ticker.C:
			frame := spinnerFrames[i%len(spinnerFrames)]
			i++
			elapsed := time.Since(s.startedAt).Round(time.Second)
			fmt.Printf("\r\x1b[K%s %s %s", WithColor(ColorCyan, frame), s.text, WithColor(ColorBrightBlack, "("+elapsed.String()+")"))
		}
	}
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
			fmt.Println(WithColor(ColorCyan, "→") + " " + s.withElapsed(s.text+" — still working"))
		}
	}
}

// halt stops the animation or heartbeat. Safe to call more than once.
func (s *StageProgress) halt() {
	if s.stop == nil {
		return
	}
	close(s.stop)
	<-s.done
	s.stop = nil
	if s.animating {
		spinnerBusy.Store(false)
	}
}

func (s *StageProgress) withElapsed(msg string) string {
	return msg + " (" + time.Since(s.startedAt).Round(time.Second).String() + ")"
}
