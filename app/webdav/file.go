/*
Copyright 2025 The Perkeep Authors.

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
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/search"
)

type perkeepDir struct {
	pfs       *perkeepFS
	path      string
	permanode blob.Ref
	children  []os.FileInfo
	readPos   int
}

func (d *perkeepDir) Close() error {
	return nil
}

func (d *perkeepDir) Read(p []byte) (n int, err error) {
	return 0, os.ErrInvalid
}

func (d *perkeepDir) Seek(offset int64, whence int) (int64, error) {
	if offset == 0 && whence == io.SeekStart {
		d.readPos = 0
		return 0, nil
	}
	return 0, os.ErrInvalid
}

func (d *perkeepDir) Write(p []byte) (n int, err error) {
	return 0, os.ErrInvalid
}

func (d *perkeepDir) Readdir(count int) ([]os.FileInfo, error) {
	if d.children == nil {
		children, err := d.fetchChildren()
		if err != nil {
			return nil, err
		}
		d.children = children
	}

	if count <= 0 {
		result := d.children[d.readPos:]
		d.readPos = len(d.children)
		return result, nil
	}

	end := d.readPos + count
	if end > len(d.children) {
		end = len(d.children)
	}

	if d.readPos >= len(d.children) {
		return nil, io.EOF
	}

	result := d.children[d.readPos:end]
	d.readPos = end

	if d.readPos >= len(d.children) {
		return result, io.EOF
	}
	return result, nil
}

func (d *perkeepDir) fetchChildren() ([]os.FileInfo, error) {
	ctx := context.Background()

	if d.path == "/" {
		return d.fetchRootChildren(ctx)
	}

	res, err := d.pfs.cl.Describe(ctx, &search.DescribeRequest{
		BlobRef: d.permanode,
		Depth:   1,
		Rules: []*search.DescribeRule{
			{
				IfResultRoot: true,
				Attrs:        []string{"camliPath:*"},
				Rules: []*search.DescribeRule{
					{Attrs: []string{"camliContent"}},
				},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to describe directory: %w", err)
	}

	db := res.Meta[d.permanode.String()]
	if db == nil || db.Permanode == nil {
		return nil, os.ErrNotExist
	}

	var children []os.FileInfo

	for k, v := range db.Permanode.Attr {
		if !strings.HasPrefix(k, "camliPath:") || len(v) == 0 {
			continue
		}

		childName := k[len("camliPath:"):]
		childRefStr := v[0]

		childDB := res.Meta[childRefStr]
		if childDB == nil {
			continue
		}

		fi := &perkeepFileInfo{
			name: childName,
		}

		if childDB.Permanode != nil {
			fi.isDir = isDirectory(childDB.Permanode)
			if !fi.isDir {
				// Get file info from the content blob
				if contentRef := childDB.Permanode.Attr.Get("camliContent"); contentRef != "" {
					if contentDB := res.Meta[contentRef]; contentDB != nil && contentDB.File != nil {
						fi.size = contentDB.File.Size
						if !contentDB.File.ModTime.IsAnyZero() {
							fi.modTime = contentDB.File.ModTime.Time()
						}
					}
				}
			}
			if fi.modTime.IsZero() && !childDB.Permanode.ModTime.IsZero() {
				fi.modTime = childDB.Permanode.ModTime
			}
		}

		fi.mode = nodeMode(fi.isDir)
		children = append(children, fi)
	}

	return children, nil
}

// fetchRootChildren returns children of the root directory.
func (d *perkeepDir) fetchRootChildren(ctx context.Context) ([]os.FileInfo, error) {
	entries, err := d.pfs.listRootEntries(ctx)
	if err != nil {
		return nil, err
	}

	children := make([]os.FileInfo, 0, len(entries))
	for name, entry := range entries {
		children = append(children, &perkeepFileInfo{
			name:    name,
			isDir:   entry.isDir,
			size:    entry.size,
			modTime: entry.modTime,
			mode:    nodeMode(entry.isDir),
		})
	}

	return children, nil
}

func (d *perkeepDir) Stat() (os.FileInfo, error) {
	return &perkeepFileInfo{
		name:    path.Base(d.path),
		mode:    os.ModeDir | 0755,
		isDir:   true,
		modTime: time.Now(),
	}, nil
}

type perkeepFileReader struct {
	pfs       *perkeepFS
	path      string
	permanode blob.Ref
	content   blob.Ref
	size      int64
	reader    *schema.FileReader
	pos       int64
}

func (f *perkeepFileReader) Close() error {
	if f.reader != nil {
		return f.reader.Close()
	}
	return nil
}

func (f *perkeepFileReader) Read(p []byte) (n int, err error) {
	if err := f.ensureReader(); err != nil {
		return 0, err
	}
	if f.reader == nil {
		return 0, io.EOF
	}
	n, err = f.reader.Read(p)
	f.pos += int64(n)
	return n, err
}

func (f *perkeepFileReader) Seek(offset int64, whence int) (int64, error) {
	if err := f.ensureReader(); err != nil {
		return 0, err
	}
	if f.reader == nil {
		switch whence {
		case io.SeekStart:
			f.pos = offset
		case io.SeekCurrent:
			f.pos += offset
		case io.SeekEnd:
			f.pos = offset
		}
		return f.pos, nil
	}
	pos, err := f.reader.Seek(offset, whence)
	if err == nil {
		f.pos = pos
	}
	return pos, err
}

func (f *perkeepFileReader) Write(p []byte) (n int, err error) {
	return 0, os.ErrInvalid
}

func (f *perkeepFileReader) Readdir(count int) ([]os.FileInfo, error) {
	return nil, os.ErrInvalid
}

func (f *perkeepFileReader) Stat() (os.FileInfo, error) {
	return &perkeepFileInfo{
		name:    path.Base(f.path),
		size:    f.size,
		mode:    0644,
		modTime: time.Now(),
		isDir:   false,
	}, nil
}

func (f *perkeepFileReader) ensureReader() error {
	if f.reader != nil || !f.content.Valid() {
		return nil
	}

	ctx := context.Background()
	reader, err := schema.NewFileReader(ctx, f.pfs.cl, f.content)
	if err != nil {
		return fmt.Errorf("failed to create file reader: %w", err)
	}
	f.reader = reader
	return nil
}

// perkeepFileWriter buffers data to a temp file and uploads on Close.
type perkeepFileWriter struct {
	pfs       *perkeepFS
	path      string
	name      string
	permanode blob.Ref
	tmp       *os.File
	size      int64
	closed    bool
}

func (f *perkeepFileWriter) Close() error {
	if f.closed {
		return nil
	}
	f.closed = true

	if f.tmp == nil {
		return f.uploadContent(nil, 0)
	}

	defer func() {
		f.tmp.Close()
		os.Remove(f.tmp.Name())
	}()

	if _, err := f.tmp.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("failed to seek temp file: %w", err)
	}

	return f.uploadContent(f.tmp, f.size)
}

func (f *perkeepFileWriter) Read(p []byte) (n int, err error) {
	return 0, os.ErrInvalid
}

func (f *perkeepFileWriter) Seek(offset int64, whence int) (int64, error) {
	if f.tmp == nil {
		return 0, nil
	}
	return f.tmp.Seek(offset, whence)
}

func (f *perkeepFileWriter) Write(p []byte) (n int, err error) {
	if f.closed {
		return 0, os.ErrClosed
	}

	if f.tmp == nil {
		tmp, err := os.CreateTemp("", "pk-webdav-")
		if err != nil {
			return 0, fmt.Errorf("failed to create temp file: %w", err)
		}
		f.tmp = tmp
	}

	n, err = f.tmp.Write(p)
	f.size += int64(n)
	return n, err
}

func (f *perkeepFileWriter) Readdir(count int) ([]os.FileInfo, error) {
	return nil, os.ErrInvalid
}

func (f *perkeepFileWriter) Stat() (os.FileInfo, error) {
	return &perkeepFileInfo{
		name:    f.name,
		size:    f.size,
		mode:    0644,
		modTime: time.Now(),
		isDir:   false,
	}, nil
}

func (f *perkeepFileWriter) uploadContent(r io.Reader, size int64) error {
	ctx := context.Background()

	var contentRef blob.Ref
	var err error

	if r != nil && size > 0 {
		contentRef, err = schema.WriteFileFromReader(ctx, f.pfs.cl, f.name, &io.LimitedReader{R: r, N: size})
		if err != nil {
			return fmt.Errorf("failed to upload file content: %w", err)
		}
	} else {
		contentRef, err = schema.WriteFileFromReader(ctx, f.pfs.cl, f.name, strings.NewReader(""))
		if err != nil {
			return fmt.Errorf("failed to upload empty file: %w", err)
		}
	}

	if err := f.pfs.signer.SetAttribute(ctx, f.permanode, "camliContent", contentRef.String()); err != nil {
		return fmt.Errorf("failed to set camliContent: %w", err)
	}

	f.pfs.invalidateCache(f.path)

	return nil
}
