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
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"strconv"
	"syscall"
	"time"
)

// killedPollInterval is how often flushContext checks whether an
// interrupted caller is being killed.
const killedPollInterval = 100 * time.Millisecond

// flushContext returns the context to use for the work done by a FLUSH
// request whose request context is ctx. The returned cancel func must be
// called when the flush is done.
//
// The fuse package cancels ctx when the kernel sends an INTERRUPT, which
// it does when the thread blocked in close(2) gets any signal, including
// the Go runtime's SIGURG preemption signals. Failing the flush then
// fails the close with EINTR, which callers can't safely retry, and
// discards the written data. So the returned context is only canceled if
// the caller is being killed, as reported by killed.
//
// The kernel sends at most one INTERRUPT per request, and programs that
// handle a signal by exiting (including Go programs on SIGINT) are only
// killed after that, so once interrupted, killed is polled until the
// flush is done.
func flushContext(ctx context.Context, killed func() bool) (context.Context, context.CancelFunc) {
	fctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	stop := context.AfterFunc(ctx, func() {
		t := time.NewTicker(killedPollInterval)
		defer t.Stop()
		for {
			if killed() {
				cancel()
				return
			}
			select {
			case <-fctx.Done():
				return
			case <-t.C:
			}
		}
	})
	return fctx, func() {
		stop()
		cancel()
	}
}

// threadKilled reports whether the thread with Linux thread ID tid (as in
// the Pid field of a fuse.Header) is being killed.
//
// When a process gets a fatal signal, the kernel queues SIGKILL to each
// of its threads, which stays pending on a thread blocked waiting for a
// FUSE reply.
func threadKilled(tid uint32) bool {
	status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", tid))
	if err != nil {
		return false
	}
	return statusHasPendingKill(status)
}

// statusHasPendingKill reports whether the contents of a
// /proc/<pid>/status file show a pending SIGKILL.
func statusHasPendingKill(status []byte) bool {
	const killBit = 1 << (syscall.SIGKILL - 1)
	sc := bufio.NewScanner(bytes.NewReader(status))
	for sc.Scan() {
		k, v, ok := bytes.Cut(sc.Bytes(), []byte(":"))
		if !ok || (string(k) != "SigPnd" && string(k) != "ShdPnd") {
			continue
		}
		mask, err := strconv.ParseUint(string(bytes.TrimSpace(v)), 16, 64)
		if err == nil && mask&killBit != 0 {
			return true
		}
	}
	return false
}
