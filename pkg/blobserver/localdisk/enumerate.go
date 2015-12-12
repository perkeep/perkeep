/*
Copyright 2011 Google Inc.

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

package localdisk

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"camlistore.org/pkg/blob"
	"golang.org/x/net/context"
)

type readBlobRequest struct {
	done    <-chan struct{}
	ch      chan<- blob.SizedRef
	after   string
	remain  *int // limit countdown
	dirRoot string

	// Not used on initial request, only on recursion
	blobPrefix, pathInto string
}

type enumerateError struct {
	msg string
	err error
}

func (ee *enumerateError) Error() string {
	return fmt.Sprintf("Enumerate error: %s: %v", ee.msg, ee.err)
}

func (ds *DiskStorage) readBlobs(opts readBlobRequest) error {
	dirFullPath := filepath.Join(opts.dirRoot, opts.pathInto)
	dir, err := os.Open(dirFullPath)
	if err != nil {
		return &enumerateError{"localdisk: opening directory " + dirFullPath, err}
	}
	names, err := dir.Readdirnames(-1)
	dir.Close()
	if err == nil && len(names) == 0 {
		// remove empty blob dir if we are in a queue but not the queue root itself
		if strings.Contains(dirFullPath, "queue-") &&
			!strings.Contains(filepath.Base(dirFullPath), "queue-") {
			go ds.tryRemoveDir(dirFullPath)
		}
		return nil
	}
	if err != nil {
		return &enumerateError{"localdisk: readdirnames of " + dirFullPath, err}
	}
	sort.Strings(names)
	stat := make(map[string]chan interface{}) // gets sent error or os.FileInfo
	for _, name := range names {
		if skipDir(name) || isShardDir(name) {
			continue
		}
		ch := make(chan interface{}, 1) // 1 in case it's not read
		name := name
		stat[name] = ch
		go func() {
			fi, err := os.Stat(filepath.Join(dirFullPath, name))
			if err != nil {
				ch <- err
			} else {
				ch <- fi
			}
		}()
	}

	for _, name := range names {
		if *opts.remain == 0 {
			return nil
		}
		if skipDir(name) {
			continue
		}
		var (
			fi      os.FileInfo
			err     error
			didStat bool
		)
		stat := func() {
			if didStat {
				return
			}
			didStat = true
			fiv := <-stat[name]
			var ok bool
			if err, ok = fiv.(error); ok {
				err = &enumerateError{"localdisk: stat of file " + filepath.Join(dirFullPath, name), err}
			} else {
				fi = fiv.(os.FileInfo)
			}
		}
		isDir := func() bool {
			stat()
			return fi != nil && fi.IsDir()
		}

		if isShardDir(name) || isDir() {
			var newBlobPrefix string
			if opts.blobPrefix == "" {
				newBlobPrefix = name + "-"
			} else {
				newBlobPrefix = opts.blobPrefix + name
			}
			if len(opts.after) > 0 {
				compareLen := len(newBlobPrefix)
				if len(opts.after) < compareLen {
					compareLen = len(opts.after)
				}
				if newBlobPrefix[:compareLen] < opts.after[:compareLen] {
					continue
				}
			}
			ropts := opts
			ropts.blobPrefix = newBlobPrefix
			ropts.pathInto = opts.pathInto + "/" + name
			if err := ds.readBlobs(ropts); err != nil {
				return err
			}
			continue
		}

		stat()
		if err != nil {
			return err
		}

		if !fi.IsDir() && strings.HasSuffix(name, ".dat") {
			blobName := strings.TrimSuffix(name, ".dat")
			if blobName <= opts.after {
				continue
			}
			if blobRef, ok := blob.Parse(blobName); ok {
				select {
				case opts.ch <- blob.SizedRef{Ref: blobRef, Size: uint32(fi.Size())}:
				case <-opts.done:
					return context.Canceled
				}
				(*opts.remain)--
			}
			continue
		}
	}

	return nil
}

func (ds *DiskStorage) EnumerateBlobs(ctx context.Context, dest chan<- blob.SizedRef, after string, limit int) error {
	defer close(dest)
	if limit == 0 {
		log.Printf("Warning: localdisk.EnumerateBlobs called with a limit of 0")
	}

	limitMutable := limit
	return ds.readBlobs(readBlobRequest{
		done:    ctx.Done(),
		ch:      dest,
		dirRoot: ds.root,
		after:   after,
		remain:  &limitMutable,
	})
}

func skipDir(name string) bool {
	// The partition directory is old. (removed from codebase, but
	// likely still on disk for some people) the "cache" and
	// "packed" directories are used by the serverconfig/genconfig
	// code, as a default location for most users.
	return name == "partition" || name == "cache" || name == "packed"
}

func isShardDir(name string) bool {
	return len(name) == 2 && isHex(name[0]) && isHex(name[1])
}

func isHex(b byte) bool {
	return ('0' <= b && b <= '9') || ('a' <= b && b <= 'f')
}
