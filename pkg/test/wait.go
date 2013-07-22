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

package test

import "time"

// WaitFor returns true if condition returns true before maxWait.
// It is checked immediately, and then every checkInterval.
func WaitFor(condition func() bool, maxWait, checkInterval time.Duration) bool {
	t0 := time.Now()
	tmax := t0.Add(maxWait)
	for time.Now().Before(tmax) {
		if condition() {
			return true
		}
		time.Sleep(checkInterval)
	}
	return false
}
