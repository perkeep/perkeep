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

// Package kvutil contains helpers related to
// github.com/cznic/kv.
package kvutil

import (
	"io"
	"os"

	"camlistore.org/third_party/github.com/camlistore/lock"
	"camlistore.org/third_party/github.com/cznic/kv"
)

// Open opens the named kv DB file for reading/writing. It
// creates the file if it does not exist yet.
func Open(filePath string, opts *kv.Options) (*kv.DB, error) {
	// TODO(mpl): use it in index pkg and such
	createOpen := kv.Open
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		createOpen = kv.Create
	}
	if opts == nil {
		opts = &kv.Options{}
	}
	if opts.Locker == nil {
		opts.Locker = func(fullPath string) (io.Closer, error) {
			return lock.Lock(filePath + ".lock")
		}
	}
	return createOpen(filePath, opts)
}
