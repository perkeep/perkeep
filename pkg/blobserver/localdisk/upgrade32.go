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

package localdisk

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

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
		oldDir := make(map[string]bool)
		log.Printf("Migrating structure of %d/%d directories in %s; doing %q", i+1, len(three), sha1root, dir)
		fullDir := filepath.Join(sha1root, dir)
		err := filepath.Walk(fullDir, func(path string, fi os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			baseName := filepath.Base(path)
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
				return fmt.Errorf("Expected %s to not exist; got stat %v, %v", fi, err)
			}
			if err := os.Rename(path, dst); err != nil {
				return err
			}
			oldDir[filepath.Dir(path)] = true
			return nil
		})
		if err != nil {
			return err
		}
		tryDel := make([]string, 0, len(oldDir))
		for dir := range oldDir {
			tryDel = append(tryDel, dir)
		}
		sort.Sort(sort.Reverse(byStringLength(tryDel)))
		for _, dir := range tryDel {
			if err := os.Remove(dir); err != nil {
				log.Printf("Failed to remove old dir %s: %v", dir, err)
			}
		}
		if err := os.Remove(fullDir); err != nil {
			log.Printf("Failed to remove old dir %s: %v", fullDir, err)
		}
	}
	return nil
}

type byStringLength []string

func (s byStringLength) Len() int           { return len(s) }
func (s byStringLength) Less(i, j int) bool { return len(s[i]) < len(s[j]) }
func (s byStringLength) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
