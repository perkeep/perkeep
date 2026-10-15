/*
Copyright 2026 The Perkeep Authors.

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

// waitParentDeath returns once the current process's parent has exited.
//
// On Windows, a process's ppid doesn't change when its parent exits, so
// this waits on a handle to the parent process instead.
func waitParentDeath(ppid int) {
	p, err := os.FindProcess(ppid)
	if err != nil {
		// The parent has most likely exited already.
		return
	}
	p.Wait()
}
