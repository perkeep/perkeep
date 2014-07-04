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

// This file deals with the migration of stuff from the old xxx/yyy/sha1-xxxyyyzzz.dat
// files to the xx/yy format.

package localdisk

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"camlistore.org/pkg/blob"
)

func (ds *DiskStorage) migrate3to2() error {
	sha1root := filepath.Join(ds.root, "sha1")
	f, err := os.Open(sha1root)
	if os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	names, err := f.Readdirnames(-1)
	if err != nil {
		return err
	}
	f.Close()
	var three []string
	for _, name := range names {
		if len(name) == 3 {
			three = append(three, name)
		}
	}
	if len(three) == 0 {
		return nil
	}
	sort.Strings(three)
	made := make(map[string]bool) // dirs made
	for i, dir := range three {
		log.Printf("Migrating structure of %d/%d directories in %s; doing %q", i+1, len(three), sha1root, dir)
		fullDir := filepath.Join(sha1root, dir)
		err := filepath.Walk(fullDir, func(path string, fi os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			baseName := filepath.Base(path)

			// Cases like "sha1-7ea231f1bd008e04c0629666ba695c399db76b8a.dat.tmp546786166"
			if strings.Contains(baseName, ".dat.tmp") && fi.Mode().IsRegular() &&
				fi.ModTime().Before(time.Now().Add(-24*time.Hour)) {
				return ds.cleanupTempFile(path)
			}

			if !(fi.Mode().IsRegular() && strings.HasSuffix(baseName, ".dat")) {
				return nil
			}
			br, ok := blob.Parse(strings.TrimSuffix(baseName, ".dat"))
			if !ok {
				return nil
			}
			dir := ds.blobDirectory(br)
			if !made[dir] {
				if err := os.MkdirAll(dir, 0700); err != nil {
					return err
				}
				made[dir] = true
			}
			dst := ds.blobPath(br)
			if fi, err := os.Stat(dst); !os.IsNotExist(err) {
				return fmt.Errorf("Expected %s to not exist; got stat %v, %v", dst, fi, err)
			}
			if err := os.Rename(path, dst); err != nil {
				return err
			}
			return nil
		})
		if err != nil {
			return err
		}
		if err := removeEmptyDirOrDirs(fullDir); err != nil {
			log.Printf("Failed to remove old dir %s: %v", fullDir, err)
		}
	}
	return nil
}

func removeEmptyDirOrDirs(dir string) error {
	err := filepath.Walk(dir, func(subdir string, fi os.FileInfo, err error) error {
		if subdir == dir {
			// root.
			return nil
		}
		if err != nil {
			return err
		}
		if fi.Mode().IsDir() {
			removeEmptyDirOrDirs(subdir)
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return err
	}
	return os.Remove(dir)
}

func (ds *DiskStorage) cleanupTempFile(path string) error {
	base := filepath.Base(path)
	i := strings.Index(base, ".dat.tmp")
	if i < 0 {
		return nil
	}
	br, ok := blob.Parse(base[:i])
	if !ok {
		return nil
	}

	// If it already exists at the good path, delete it.
	goodPath := ds.blobPath(br)
	if _, err := os.Stat(goodPath); err == nil {
		return os.Remove(path)
	}

	// TODO(bradfitz): care whether it's correct digest or not?
	return nil
}
