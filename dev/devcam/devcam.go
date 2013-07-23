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

package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"camlistore.org/pkg/cmdmain"
)

func setenv(key, value string) {
	err := os.Setenv(key, value)
	if err != nil {
		log.Fatalf("Could not set env var %v to %v: %v", key, value, err)
	}
}

func cpDir(src, dst string) error {
	return filepath.Walk(src, func(fullpath string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		suffix, err := filepath.Rel(src, fullpath)
		if err != nil {
			return fmt.Errorf("Failed to find Rel(%q, %q): %v", src, fullpath, err)
		}
		if fi.IsDir() {
			return nil
		}
		return cpFile(fullpath, filepath.Join(dst, suffix))
	})
}

func cpFile(src, dst string) error {
	sfi, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !sfi.Mode().IsRegular() {
		return fmt.Errorf("cpFile can't deal with non-regular file %s", src)
	}

	dstDir := filepath.Dir(dst)
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return err
	}

	df, err := os.Create(dst)
	if err != nil {
		return err
	}
	sf, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sf.Close()

	n, err := io.Copy(df, sf)
	if err == nil && n != sfi.Size() {
		err = fmt.Errorf("copied wrong size for %s -> %s: copied %d; want %d", src, dst, n, sfi.Size())
	}
	cerr := df.Close()
	if err == nil {
		err = cerr
	}
	return err
}

func main() {
	cmdmain.Main()
}
