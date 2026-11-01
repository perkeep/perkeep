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
	"fmt"
	"runtime"
	"slices"
	"strings"
	"time"
)

// lockWaitReport is how long Lock waits for the lock before logging the
// index's read lock holders.
var lockWaitReport = 30 * time.Second

// readLockHolder is a read lock of an Index that hasn't been unlocked.
type readLockHolder struct {
	since time.Time
	pcs   []uintptr // the stack of RLock's caller
}

// Lock write-locks the index. If that takes longer than lockWaitReport,
// it logs the read lock holders that it's waiting for.
func (x *Index) Lock() {
	if x.mu.TryLock() {
		return
	}
	t := time.AfterFunc(lockWaitReport, x.logReadLockHolders)
	x.mu.Lock()
	t.Stop()
}

func (x *Index) Unlock() { x.mu.Unlock() }

// RLock read-locks the index, and returns the func that unlocks it,
// which must be called exactly once.
//
// Until then, the index keeps the stack of RLock's caller, for Lock to
// log if it waits long for the lock.
func (x *Index) RLock() (unlock func()) {
	pcs := make([]uintptr, 32)
	pcs = pcs[:runtime.Callers(2, pcs)]
	x.mu.RLock()
	h := &readLockHolder{since: time.Now(), pcs: pcs}
	x.readLockHoldersMu.Lock()
	x.readLockHolders.Make()
	x.readLockHolders.Add(h)
	x.readLockHoldersMu.Unlock()
	return func() {
		x.readLockHoldersMu.Lock()
		held := x.readLockHolders.Contains(h)
		x.readLockHolders.Delete(h)
		x.readLockHoldersMu.Unlock()
		if !held {
			panic("index: read lock unlocked twice")
		}
		x.mu.RUnlock()
	}
}

// logReadLockHolders logs the index's read lock holders and their stacks,
// oldest first.
func (x *Index) logReadLockHolders() {
	x.readLockHoldersMu.Lock()
	holders := x.readLockHolders.Slice()
	x.readLockHoldersMu.Unlock()
	slices.SortFunc(holders, func(a, b *readLockHolder) int { return a.since.Compare(b.since) })

	var sb strings.Builder
	fmt.Fprintf(&sb, "index: Lock has waited %v; %d read lock holders:", lockWaitReport, len(holders))
	now := time.Now()
	for _, h := range holders {
		fmt.Fprintf(&sb, "\nread-locked %v ago by:\n", now.Sub(h.since).Round(time.Millisecond))
		frames := runtime.CallersFrames(h.pcs)
		for {
			f, more := frames.Next()
			fmt.Fprintf(&sb, "\t%s\n\t\t%s:%d\n", f.Function, f.File, f.Line)
			if !more {
				break
			}
		}
	}
	x.printf("%s", sb.String())
}
