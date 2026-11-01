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

package index

import (
	"log"
	"strings"
	"testing"
	"time"
)

// chanWriter sends each write to a channel.
type chanWriter chan string

func (w chanWriter) Write(p []byte) (int, error) {
	w <- string(p)
	return len(p), nil
}

// holdReadLock read-locks x, for TestLockLogsReadLockHolders to find in
// the holder's stack.
func holdReadLock(x *Index) (unlock func()) {
	return x.RLock()
}

func TestLockLogsReadLockHolders(t *testing.T) {
	old := lockWaitReport
	lockWaitReport = time.Millisecond
	t.Cleanup(func() { lockWaitReport = old })

	x := NewMemoryIndex()
	logs := make(chanWriter, 1)
	x.Logger = log.New(logs, "", 0)

	unlock := holdReadLock(x)
	locked := make(chan struct{})
	go func() {
		x.Lock()
		close(locked)
	}()
	msg := <-logs
	if !strings.Contains(msg, "1 read lock holders") || !strings.Contains(msg, "index.holdReadLock") {
		t.Errorf("log doesn't name the read lock holder:\n%s", msg)
	}
	unlock()
	<-locked
	x.Unlock()

	// With the holder gone, nothing more is logged.
	select {
	case msg := <-logs:
		t.Errorf("unexpected log after unlocking:\n%s", msg)
	default:
	}
	x.readLockHoldersMu.Lock()
	n := x.readLockHolders.Len()
	x.readLockHoldersMu.Unlock()
	if n != 0 {
		t.Errorf("%d read lock holders after unlocking; want 0", n)
	}
}

func TestRLockUnlockTwice(t *testing.T) {
	x := NewMemoryIndex()
	unlock := x.RLock()
	unlock()
	defer func() {
		if recover() == nil {
			t.Error("unlocking twice didn't panic")
		}
	}()
	unlock()
}
