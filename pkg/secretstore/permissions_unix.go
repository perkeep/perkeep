//go:build !windows

/*
Copyright 2026 The Perkeep Authors.

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

package secretstore

import "os"

func preparePrivateDirectory(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return ErrUnsafePath
	}
	fi, err := os.Lstat(dir)
	if err != nil || !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 || fi.Mode().Perm()&0022 != 0 {
		return ErrUnsafePath
	}
	return nil
}

func openPrivateFile(file string, flags int) (*os.File, error) {
	return os.OpenFile(file, flags, 0600)
}

func protectPrivateFile(f *os.File) error {
	return f.Chmod(0600)
}
