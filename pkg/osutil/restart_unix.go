// +build linux darwin

// TODO(mpl): Copyright in next CL.

package osutil

import (
	"os"
	"syscall"
)

// restartProcess returns an error if things couldn't be
// restarted.  On success, this function never returns
// because the process becomes the new process.
func RestartProcess() error {
	return syscall.Exec(os.Args[0], os.Args, os.Environ())
}
