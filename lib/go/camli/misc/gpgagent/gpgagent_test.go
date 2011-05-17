/*
Copyright 2011 Google Inc.

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

package gpgagent

import (
	"os"
	"testing"
	"time"
)

func TestPrompt(t *testing.T) {
	if os.Getenv("TEST_GPGAGENT_LIB") != "1" {
		t.Logf("skipping TestPrompt without $TEST_GPGAGENT_LIB == 1")
		return
	}
	req := &PassphraseRequest{
		CacheKey: "gpgagent_test-cachekey",
	}
	s1, err := req.GetPassphrase()
	if err != nil {
		t.Fatal(err)
	}
	t1 := time.Nanoseconds()
	s2, err := req.GetPassphrase()
	if err != nil {
		t.Fatal(err)
	}
	t2 := time.Nanoseconds()
	if td := t2 - t1; td > 1e9/5 {
		t.Errorf("cached passphrase took more than 1/5 second; took %d ns", td)
	}
	if s1 != s2 {
		t.Errorf("cached passphrase differed; got %q, want %q", s2, s1)
	}
}
