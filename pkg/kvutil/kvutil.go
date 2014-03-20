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
	"fmt"
	"io"
	"os"
	"strconv"

	"camlistore.org/third_party/github.com/camlistore/lock"
	"camlistore.org/third_party/github.com/cznic/kv"
)

// Open opens the named kv DB file for reading/writing. It
// creates the file if it does not exist yet.
func Open(dbFile string, opts *kv.Options) (*kv.DB, error) {
	createOpen := kv.Open
	verb := "opening"
	if _, err := os.Stat(dbFile); os.IsNotExist(err) {
		createOpen = kv.Create
		verb = "creating"
	}
	if opts == nil {
		opts = &kv.Options{}
	}
	if opts.Locker == nil {
		opts.Locker = func(dbFile string) (io.Closer, error) {
			lkfile := dbFile + ".lock"
			cl, err := lock.Lock(lkfile)
			if err != nil {
				return nil, fmt.Errorf("failed to acquire lock on %s: %v", lkfile, err)
			}
			return cl, nil
		}
	}
	if v, _ := strconv.ParseBool(os.Getenv("CAMLI_KV_VERIFY")); v {
		opts.VerifyDbBeforeOpen = true
		opts.VerifyDbAfterOpen = true
		opts.VerifyDbBeforeClose = true
		opts.VerifyDbAfterClose = true
	}
	db, err := createOpen(dbFile, opts)
	if err != nil {
		return nil, fmt.Errorf("error %s %s: %v", verb, dbFile, err)
	}
	return db, nil
}
