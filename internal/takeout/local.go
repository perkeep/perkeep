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

package takeout

import (
	"context"
	"io"
	"os"
	"path/filepath"
)

// LocalExports returns the Takeout exports whose zip files are in dir.
func LocalExports(dir string) ([]*Export, error) {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var zips []*Zip
	for _, de := range des {
		if !de.Type().IsRegular() {
			continue
		}
		if _, _, ok := exportName(de.Name()); !ok {
			continue
		}
		fi, err := de.Info()
		if err != nil {
			return nil, err
		}
		zips = append(zips, &Zip{
			Name: de.Name(),
			Size: fi.Size(),
			src:  localZip(filepath.Join(dir, de.Name())),
		})
	}
	return groupExports(zips), nil
}

// localZip is the path of a zip file on local disk.
type localZip string

func (lz localZip) readTOC(ctx context.Context) ([]byte, error) {
	f, err := os.Open(string(lz))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	return readTOC(ctx, fi.Size(), func(_ context.Context, off, n int64) ([]byte, error) {
		buf := make([]byte, n)
		_, err := f.ReadAt(buf, off)
		return buf, err
	})
}

func (lz localZip) openRange(ctx context.Context, off, n int64) (io.ReadCloser, error) {
	f, err := os.Open(string(lz))
	if err != nil {
		return nil, err
	}
	return struct {
		io.Reader
		io.Closer
	}{io.NewSectionReader(f, off, n), f}, nil
}
