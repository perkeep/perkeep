/*
Copyright 2013 Google Inc.

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

package leak

import (
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLeak(t *testing.T) {
	testLeak(t, true, 1)
}

func TestNoLeak(t *testing.T) {
	testLeak(t, false, 0)
}

func testLeak(t *testing.T, leak bool, want int) {
	defer func() {
		testHookFinalize = nil
		onLeak = logLeak
	}()
	var mu sync.Mutex // guards leaks
	var leaks []string
	onLeak = func(_ *Checker, stack string) {
		mu.Lock()
		defer mu.Unlock()
		leaks = append(leaks, stack)
	}
	finalizec := make(chan bool)
	testHookFinalize = func() {
		finalizec <- true
	}

	c := make(chan bool)
	go func() {
		ch := NewChecker()
		if !leak {
			ch.Close()
		}
		c <- true
	}()
	<-c
	go runtime.GC()
	select {
	case <-time.After(5 * time.Second):
		t.Error("timeout waiting for finalization")
	case <-finalizec:
	}
	mu.Lock() // no need to unlock
	if len(leaks) != want {
		t.Errorf("got %d leaks; want %d", len(leaks), want)
	}
	if len(leaks) == 1 && !strings.Contains(leaks[0], "leak_test.go") {
		t.Errorf("Leak stack doesn't contain leak_test.go: %s", leaks[0])
	}
}
