/*
Copyright 2011 The Perkeep Authors

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

package files

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"perkeep.org/pkg/blob"
)

type readBlobRequest struct {
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
	return fmt.Sprintf("files enumerate error: %s: %v", ee.msg, ee.err)
}

// readBlobs implements EnumerateBlobs. It calls itself recursively on subdirectories.
func (ds *Storage) readBlobs(ctx context.Context, opts readBlobRequest) error {
	dirFullPath := filepath.Join(opts.dirRoot, opts.pathInto)
	names, err := ds.fs.ReadDirNames(dirFullPath)
	if err != nil {
		return &enumerateError{"readdirnames of " + dirFullPath, err}
	}
	if len(names) == 0 {
		// remove empty blob dir if we are in a queue but not the queue root itself
		if strings.Contains(dirFullPath, "queue-") &&
			!strings.Contains(filepath.Base(dirFullPath), "queue-") {
			go ds.tryRemoveDir(dirFullPath)
		}
		return nil
	}
	sort.Strings(names)
	stat := make(map[string]*future) // name -> future<os.FileInfo>

	var toStat []func()
	for _, name := range names {
		if skipDir(name) || isShardDir(name) {
			continue
		}
		fullFile := filepath.Join(dirFullPath, name)
		f := newFuture(func() (os.FileInfo, error) {
			fi, err := ds.fs.Stat(fullFile)
			if err != nil {
				return nil, &enumerateError{"stat", err}
			}
			return fi, nil
		})
		stat[name] = f
		toStat = append(toStat, f.ForceLoad)
	}

	// Start pre-statting things.
	go func() {
		for _, f := range toStat {
			ds.statGate.Start()
			f := f
			go func() {
				ds.statGate.Done()
				f()
			}()
		}
	}()

	for _, name := range names {
		if *opts.remain == 0 {
			return nil
		}
		if skipDir(name) {
			continue
		}

		isDir := isShardDir(name)
		if !isDir {
			fi, err := stat[name].Get()
			if err != nil {
				return err
			}
			isDir = fi.IsDir()
		}

		if isDir {
			var newBlobPrefix string
			if opts.blobPrefix == "" {
				newBlobPrefix = name + "-"
			} else {
				newBlobPrefix = opts.blobPrefix + name
			}
			if len(opts.after) > 0 {
				compareLen := min(len(opts.after), len(newBlobPrefix))
				if newBlobPrefix[:compareLen] < opts.after[:compareLen] {
					continue
				}
			}
			ropts := opts
			ropts.blobPrefix = newBlobPrefix
			ropts.pathInto = opts.pathInto + "/" + name
			if err := ds.readBlobs(ctx, ropts); err != nil {
				return err
			}
			continue
		}

		if !strings.HasSuffix(name, ".dat") {
			continue
		}

		fi, err := stat[name].Get()
		if err != nil {
			return err
		}

		if !fi.IsDir() {
			blobName := strings.TrimSuffix(name, ".dat")
			if blobName <= opts.after {
				continue
			}
			if blobRef, ok := blob.Parse(blobName); ok {
				select {
				case opts.ch <- blob.SizedRef{Ref: blobRef, Size: uint32(fi.Size())}:
					(*opts.remain)--
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		}
	}

	return nil
}

func (ds *Storage) EnumerateBlobs(ctx context.Context, dest chan<- blob.SizedRef, after string, limit int) error {
	defer close(dest)
	if limit == 0 {
		log.Printf("Warning: files.EnumerateBlobs called with a limit of 0")
	}

	limitMutable := limit
	return ds.readBlobs(ctx, readBlobRequest{
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

// future is an os.FileInfo future.
type future struct {
	once sync.Once
	f    func() (os.FileInfo, error)
	v    os.FileInfo
	err  error
}

func newFuture(f func() (os.FileInfo, error)) *future {
	return &future{f: f}
}

func (f *future) Get() (os.FileInfo, error) {
	f.once.Do(f.run)
	return f.v, f.err
}

func (f *future) ForceLoad() {
	f.Get()
}

func (f *future) run() { f.v, f.err = f.f() }
