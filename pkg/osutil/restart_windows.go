// +build windows

// TODO(mpl): Copyright in next CL.

package osutil

import (
	"log"
)

// restartProcess returns an error if things couldn't be
// restarted.  On success, this function never returns
// because the process becomes the new process.
func RestartProcess() error {
	log.Print("RestartProcess not implemented on windows")
	return nil
}
