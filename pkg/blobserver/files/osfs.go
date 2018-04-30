/*
Copyright 2018 The Perkeep Authors

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
	"io/ioutil"
	"os"
)

// OSFS returns an implementation of VFS interface using the host filesystem.
func OSFS() VFS {
	return osFS{}
}

// osFS implements the VFS interface using the os package and
// the host filesystem.
type osFS struct{}

func (osFS) Remove(path string) error                     { return os.Remove(path) }
func (osFS) RemoveDir(path string) error                  { return os.Remove(path) }
func (osFS) Stat(path string) (os.FileInfo, error)        { return os.Stat(path) }
func (osFS) Lstat(path string) (os.FileInfo, error)       { return os.Lstat(path) }
func (osFS) Open(path string) (ReadableFile, error)       { return os.Open(path) }
func (osFS) MkdirAll(path string, perm os.FileMode) error { return os.MkdirAll(path, perm) }

func (osFS) Rename(oldname, newname string) error {
	err := os.Rename(oldname, newname)
	if err != nil {
		err = mapRenameError(err, oldname, newname)
	}
	return err
}

func (osFS) TempFile(dir, prefix string) (WritableFile, error) {
	f, err := ioutil.TempFile(dir, prefix)
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (osFS) ReadDirNames(dir string) ([]string, error) {
	d, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	return d.Readdirnames(-1)
}
