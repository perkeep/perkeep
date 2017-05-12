// +build !windows,!appengine,!solaris

/*
Copyright 2013 The Camlistore Authors.

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
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
)

func mkfifo(path string, mode uint32) error {
	return syscall.Mkfifo(path, mode)
}

func mksocket(path string) error {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	tmp := filepath.Join(dir, "."+base)
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: tmp, Net: "unix"})
	if err != nil {
		return err
	}

	err = os.Rename(tmp, path)
	if err != nil {
		l.Close()
		os.Remove(tmp) // Ignore error
		return err
	}

	l.Close()

	return nil
}

func maxFD() (uint64, error) {
	var rlim syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &rlim); err != nil {
		if err == syscall.ENOSYS {
			// syscall.Getrlimit() not implemented in ARMv5, and it returns this string
			return 0, ErrNotSupported
		}
		return 0, fmt.Errorf("ulimit error: %v", err)
	}
	// On FreeBSD Getrlimit returns an int64, because (among other things) the
	// maximum value for the Rlimit struct fields, called RLIM_INFINITY used to
	// be defined as -1.
	// According to
	// https://github.com/freebsd/freebsd/blob/master/sys/sys/resource.h , it looks
	// like it is now defined as
	// #define	RLIM_INFINITY	((rlim_t)(((__uint64_t)1 << 63) - 1))
	// which is the maximum positive value of an int64. So casting to an uint64
	// should be ok.
	return uint64(rlim.Cur), nil
}
