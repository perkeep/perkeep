/*
Copyright 2013 The Camlistore Authors

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

package syncutil

import (
	"log"
	"runtime"
	"sync"
)

// RWMutexTracker is a sync.RWMutex that tracks who owns the current
// exclusive lock.  It's used for debugging deadlocks.
type RWMutexTracker struct {
	mu sync.RWMutex

	hmu    sync.Mutex
	holder []byte
}

const stackBufSize = 16 << 20

func (m *RWMutexTracker) Lock() {
	m.mu.Lock()

	m.hmu.Lock()
	if len(m.holder) == 0 {
		m.holder = make([]byte, stackBufSize)
	}
	m.holder = m.holder[:runtime.Stack(m.holder[:stackBufSize], false)]
	log.Printf("Lock at %s", string(m.holder))
	m.hmu.Unlock()
}

func (m *RWMutexTracker) Unlock() {
	m.hmu.Lock()
	m.holder = m.holder[:0]
	m.hmu.Unlock()
	m.mu.Unlock()
}

func (m *RWMutexTracker) RLock()   { m.mu.RLock() }
func (m *RWMutexTracker) RUnlock() { m.mu.RUnlock() }

// Holder returns the stack trace of the current exclusive lock holder's stack
// when it acquired the lock (with Lock). It returns the empty string if the lock
// is not currently held.
func (m *RWMutexTracker) Holder() string {
	m.hmu.Lock()
	defer m.hmu.Unlock()
	return string(m.holder)
}
