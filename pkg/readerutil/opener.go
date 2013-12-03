/*
Copyright 2013 The Camlistore Authors

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

package readerutil

import (
	"os"
	"sync"
	"sync/atomic"

	"camlistore.org/pkg/singleflight"
	"camlistore.org/pkg/types"
)

var (
	openerGroup singleflight.Group

	openFileMu sync.RWMutex // guards openFiles
	openFiles  = make(map[string]*openFile)
)

type openFile struct {
	// refCount must be 64-bit aligned for 32-bit platforms.
	refCount int64 // starts at 1; only valid if initial increment >= 2

	*os.File
	path string // map key of openFiles
}

func (f *openFile) Close() error {
	if atomic.AddInt64(&f.refCount, -1) == 0 {
		openFileMu.Lock()
		if openFiles[f.path] == f {
			delete(openFiles, f.path)
		}
		openFileMu.Unlock()
		f.File.Close()
	}
	return nil
}

// OpenSingle opens the given file path for reading, reusing existing file descriptors
// when possible.
func OpenSingle(path string) (types.ReaderAtCloser, error) {
	// Returns an *openFile
	resi, err := openerGroup.Do(path, func() (interface{}, error) {
		openFileMu.RLock()
		of := openFiles[path]
		openFileMu.RUnlock()
		if of != nil {
			if atomic.AddInt64(&of.refCount, 1) >= 2 {
				return of, nil
			}
			of.Close()
		}

		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		of = &openFile{
			File:     f,
			refCount: 1,
			path:     path,
		}
		openFileMu.Lock()
		openFiles[path] = of
		openFileMu.Unlock()
		return of, nil
	})
	if err != nil {
		return nil, err
	}
	return resi.(*openFile), nil
}
