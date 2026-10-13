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

import (
	"os"

	"golang.org/x/sys/unix"
)

// waitParentExit waits for the current process's parent, ppid, to exit,
// using a pidfd, and reports whether it did. It reports false without
// waiting if it can't use a pidfd (as on kernels older than 5.3), or if
// ppid is no longer the parent once the pidfd is open, in which case
// ppid may already belong to an unrelated process.
//
// Unlike PR_SET_PDEATHSIG, which fires when the parent thread that
// started this process exits, this waits for the whole parent process.
func waitParentExit(ppid int) bool {
	fd, err := unix.PidfdOpen(ppid, 0)
	if err != nil {
		return false
	}
	defer unix.Close(fd)
	if os.Getppid() != ppid {
		return false
	}
	// A pidfd becomes readable when its process exits.
	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	for {
		_, err := unix.Poll(fds, -1)
		if err != unix.EINTR {
			return err == nil
		}
	}
}
