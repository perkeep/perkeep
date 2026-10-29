/*
Copyright 2026 The Perkeep Authors

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

package main

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/client"
	"perkeep.org/pkg/client/android"
	"perkeep.org/pkg/cmdmain"
	"perkeep.org/pkg/upload"
)

// testHookNoUploadTree, if set, makes directories be uploaded with
// TreeUpload, for tests comparing it with upload.Tree.
var testHookNoUploadTree bool

// testHookFilesViaStatReceiver, if set, makes regular files be uploaded
// through the stat receiver, rather than with package upload, for tests
// comparing them.
var testHookFilesViaStatReceiver bool

// canUploadTree reports whether directories can be uploaded with
// upload.Tree, rather than TreeUpload, which does what it doesn't: file
// permanodes, EXIF times, counting blobs instead of uploading them
// (-memstats), and reporting progress to the Android app.
func (up *Uploader) canUploadTree() bool {
	return !testHookNoUploadTree &&
		!up.fileOpts.wantFilePermanode() &&
		(up.fileOpts == nil || !up.fileOpts.exifTime) &&
		up.altStatReceiver == nil &&
		!android.IsChild()
}

// uploadTree uploads the directory tree at dir with upload.Tree,
// returning the result for its root directory schema blob.
func (up *Uploader) uploadTree(ctx context.Context, dir string) (*client.PutResult, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	root := filepath.VolumeName(abs) + string(filepath.Separator)
	rel := filepath.ToSlash(strings.TrimPrefix(abs, root))
	if rel == "" {
		rel = "."
	}
	opts := &upload.TreeOptions{
		Limiter: up.limiter,
		Ignore: func(p string, fi fs.FileInfo) bool {
			return up.Client.IsIgnoredFile(treeArgPath(dir, rel, p))
		},
	}
	if up.fileOpts != nil {
		opts.File.ContentsOnly = up.fileOpts.contentsOnly
		opts.File.CapCtime = up.fileOpts.capCtime
	}
	if up.statCache != nil {
		opts.Cache = treeStatCache{up.statCache, up.pwd, root}
	}
	t := upload.NewTree(up.Client, os.DirFS(root), rel, opts)

	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				logTreeProgress(t.Progress())
			}
		}
	}()
	var progressDone func(error)
	if up.progress != nil {
		progressDone = up.progress.add(dir, t)
	}
	res, err := t.Run(ctx)
	if progressDone != nil {
		progressDone(err)
	}
	if err != nil {
		return nil, err
	}
	logTreeProgress(t.Progress())
	return &client.PutResult{BlobRef: res.DirRef}, nil
}

// treeArgPath returns the path of p, a path in a tree's fs.FS whose root
// directory is at rel in it, as TreeUpload has always formed it: under
// dir, the directory as given on the command line. It's for the client's
// rules of ignored files, which may match any of a path's components: an
// absolute path's would include those of the directory's parents.
func treeArgPath(dir, rel, p string) string {
	if rel != "." {
		p = strings.TrimPrefix(strings.TrimPrefix(p, rel), "/")
	}
	return filepath.Join(dir, filepath.FromSlash(p))
}

func logTreeProgress(p upload.TreeProgress) {
	cmdmain.Logf("FILES: %d/%d done (%d cached), %d/%d bytes; DIRS: %d/%d done",
		p.FilesDone, p.Files, p.FilesCached, p.BytesDone, p.Bytes, p.DirsDone, p.Dirs)
}

// treeStatCache is the upload.StatCache of an UploadCache, of the files of
// a tree in the os.DirFS of root.
type treeStatCache struct {
	c         UploadCache
	pwd, root string
}

func (sc treeStatCache) path(p string) string { return filepath.Join(sc.root, filepath.FromSlash(p)) }

func (sc treeStatCache) FileRef(p string, fi fs.FileInfo) (blob.Ref, bool) {
	pr, err := sc.c.CachedPutResult(sc.pwd, sc.path(p), fi, false)
	if err != nil {
		return blob.Ref{}, false
	}
	return pr.BlobRef, true
}

func (sc treeStatCache) AddFileRef(p string, fi fs.FileInfo, fileRef blob.Ref) {
	sc.c.AddCachedPutResult(sc.pwd, sc.path(p), fi, &client.PutResult{BlobRef: fileRef}, false)
}
