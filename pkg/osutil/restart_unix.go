// +build linux darwin freebsd netbsd openbsd

/*
Copyright 2012 The Camlistore Authors.

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
	"errors"
	"os"
	"runtime"
	"syscall"
)

// if non-nil, osSelfPath is used from selfPath.
var osSelfPath func() (string, error)

func selfPath() (string, error) {
	if f := osSelfPath; f != nil {
		return f()
	}
	switch runtime.GOOS {
	case "linux":
		return "/proc/self/exe", nil
	case "netbsd":
		return "/proc/curproc/exe", nil
	case "openbsd":
		return "/proc/curproc/file", nil
	case "darwin":
		// TODO(mpl): maybe do the right thing for darwin too, but that may require changes to runtime.
		// See https://codereview.appspot.com/6736069/
		return os.Args[0], nil
	}
	return "", errors.New("No restart because selfPath() not implemented for " + runtime.GOOS)
}

// restartProcess returns an error if things couldn't be
// restarted.  On success, this function never returns
// because the process becomes the new process.
func RestartProcess() error {
	path, err := selfPath()
	if err != nil {
		return err
	}
	return syscall.Exec(path, os.Args, os.Environ())
}
