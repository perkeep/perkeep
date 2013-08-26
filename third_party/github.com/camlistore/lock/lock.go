/*
Copyright 2013 The Go Authors

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

package lock

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// Lock locks the given file, creating the file if necessary. If the
// file already exists, it must have zero size or an error is returned.
// The lock is an exclusive lock (a write lock), but locked files
// should neither be read from nor written to. Such files should have
// zero size and only exist to co-ordinate ownership across processes.
//
// A nil Closer is returned if an error occurred. Otherwise, close that
// Closer to release the lock.
//
// On Linux and OSX, a lock has the same semantics as fcntl(2)'s advisory
// locks.  In particular, closing any other file descriptor for the same
// file will release the lock prematurely.
//
// Attempting to lock a file that is already locked by the current process
// has undefined behavior.
//
// Lock is not yet implemented on other operating systems, and calling it
// will return an error.
func Lock(name string) (io.Closer, error) {
	return lockFn(name)
}

var lockFn = lockPortable

// Portable version not using fcntl. Doesn't handle crashes as gracefully,
// since it can leave stale lock files.
// TODO: write pid of owner to lock file and on race see if pid is
// still alive?
func lockPortable(name string) (io.Closer, error) {
	absName, err := filepath.Abs(name)
	if err != nil {
		return nil, fmt.Errorf("can't Lock file %q: can't find abs path: %v", name, err)
	}
	fi, err := os.Stat(absName)
	if err == nil && fi.Size() > 0 {
		return nil, fmt.Errorf("can't Lock file %q: has non-zero size", name)
	}
	f, err := os.OpenFile(absName, os.O_RDWR|os.O_CREATE|os.O_TRUNC|os.O_EXCL, 0666)
	if err != nil {
		return nil, fmt.Errorf("failed to create lock file %s %v", absName, err)
	}
	return &lockCloser{f: f, abs: absName}, nil
}

type lockCloser struct {
	f    *os.File
	abs  string
	once sync.Once
	err  error
}

func (lc *lockCloser) Close() error {
	lc.once.Do(lc.close)
	return lc.err
}

func (lc *lockCloser) close() {
	if err := lc.f.Close(); err != nil {
		lc.err = err
	}
	if err := os.Remove(lc.abs); err != nil {
		lc.err = err
	}
}
