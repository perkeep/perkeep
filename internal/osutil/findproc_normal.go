/*
Copyright 2013 The Perkeep Authors.

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

package osutil

import "os"

// DieOnParentDeath starts a goroutine that waits for the current
// process's parent to exit, and calls os.Exit(0) as soon as it does.
func DieOnParentDeath() {
	// Capture the parent before scheduling the watcher. If the parent exits
	// first, a Unix watcher could otherwise start by observing the reaper.
	ppid := os.Getppid()
	go func() {
		waitParentDeath(ppid)
		os.Exit(0)
	}()
}
