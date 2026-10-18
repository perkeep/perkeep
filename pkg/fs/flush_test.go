//go:build linux

/*
Copyright 2026 The Perkeep Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package fs

import (
	"context"
	"fmt"
	"sync/atomic"
	"syscall"
	"testing"
	"testing/synctest"
	"time"
)

func TestFlushContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var killed, checked atomic.Bool
		reqCtx, interrupt := context.WithCancel(context.Background())
		ctx, cancel := flushContext(reqCtx, func() bool {
			checked.Store(true)
			return killed.Load()
		})
		defer cancel()

		synctest.Wait()
		if checked.Load() {
			t.Fatal("caller checked before any interrupt")
		}

		interrupt()
		synctest.Wait()
		if !checked.Load() {
			t.Fatal("caller not checked after interrupt")
		}
		if err := ctx.Err(); err != nil {
			t.Fatalf("flush context canceled by interrupt of live caller: %v", err)
		}

		time.Sleep(10 * killedPollInterval)
		synctest.Wait()
		if err := ctx.Err(); err != nil {
			t.Fatalf("flush context canceled while caller alive: %v", err)
		}

		killed.Store(true)
		time.Sleep(killedPollInterval)
		synctest.Wait()
		if ctx.Err() == nil {
			t.Fatal("flush context not canceled after caller killed")
		}
	})
}

func TestFlushContextDone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reqCtx, interrupt := context.WithCancel(context.Background())
		_, cancel := flushContext(reqCtx, func() bool { return false })
		interrupt()
		synctest.Wait()
		cancel()
		// synctest.Test fails if the polling goroutine is left running.
	})
}

func TestStatusHasPendingKill(t *testing.T) {
	const status = "Name:\tfoo\nState:\tD (disk sleep)\nSigQ:\t0/63432\nSigPnd:\t%s\nShdPnd:\t%s\nSigBlk:\t0000000000000000\n"
	tests := []struct {
		sigPnd, shdPnd string
		want           bool
	}{
		{"0000000000000000", "0000000000000000", false},
		{"0000000000000100", "0000000000000000", true},
		{"0000000000000000", "0000000000000100", true},
		{"0000000000000002", "0000000000000000", false}, // SIGINT only
		{"0000000080000100", "0000000000000000", true},
	}
	for _, tt := range tests {
		s := []byte(fmt.Sprintf(status, tt.sigPnd, tt.shdPnd))
		if got := statusHasPendingKill(s); got != tt.want {
			t.Errorf("SigPnd %s, ShdPnd %s: got %v, want %v", tt.sigPnd, tt.shdPnd, got, tt.want)
		}
	}
}

func TestThreadKilled(t *testing.T) {
	if threadKilled(uint32(syscall.Gettid())) {
		t.Error("threadKilled(self) = true")
	}
	if threadKilled(0) {
		t.Error("threadKilled(0) = true")
	}
}
