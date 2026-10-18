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

package searchtypes

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestCachedMatcher(t *testing.T) {
	c := new(Constraint)
	var calls atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			got := CachedMatcher(c, func() any { calls.Add(1); return 42 })
			if got != 42 {
				t.Errorf("CachedMatcher = %v; want 42", got)
			}
		})
	}
	wg.Wait()
	if got := calls.Load(); got != 1 {
		t.Errorf("matcher initialized %d times; want 1", got)
	}
}

func TestStringRegexpCache(t *testing.T) {
	c := &StringConstraint{Regexp: "a+"}
	first, err := StringRegexp(c)
	if err != nil {
		t.Fatal(err)
	}
	second, err := StringRegexp(c)
	if err != nil || second != first {
		t.Errorf("second compile = %p, %v; want cached %p", second, err, first)
	}
	invalid := &StringConstraint{Regexp: "["}
	if _, err := StringRegexp(invalid); err == nil {
		t.Fatal("invalid regexp compiled")
	}
	invalid.Regexp = "valid"
	if _, err := StringRegexp(invalid); err != nil {
		t.Fatalf("failed compile was cached: %v", err)
	}
}

func TestStringRegexpConcurrent(t *testing.T) {
	c := &StringConstraint{Regexp: "a+"}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			<-start
			re, err := StringRegexp(c)
			if err != nil {
				t.Errorf("StringRegexp: %v", err)
			} else if !re.MatchString("aaa") || re.MatchString("bbb") {
				t.Error("compiled regexp does not match the constraint")
			}
		})
	}
	close(start)
	wg.Wait()
	cached := c.regexp.Load()
	if cached == nil {
		t.Fatal("regexp was not cached")
	}
	if re, err := StringRegexp(c); err != nil || re != cached {
		t.Errorf("StringRegexp = %p, %v; want cached %p", re, err, cached)
	}
}
