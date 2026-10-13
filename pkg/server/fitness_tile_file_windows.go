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

package server

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func fitnessTileOpenFile(path string) (*os.File, error) {
	// Unlike os.Open, CreateFile needs an extended absolute path to support
	// cache directories whose record names take them beyond MAX_PATH.
	name, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(name, `\\?\`) && !strings.HasPrefix(name, `\\.\`) {
		if strings.HasPrefix(name, `\\`) {
			name = `\\?\UNC\` + name[2:]
		} else {
			name = `\\?\` + name
		}
	}
	namep, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	// Readers must share deletion as well as reads/writes so that a
	// POSIX-semantics rename can replace their previous envelope.
	// Open the reparse point itself: readRecord rejects non-regular handles,
	// including a symlink substituted after its initial Lstat.
	handle, err := windows.CreateFile(namep, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(handle), path), nil
}

func fitnessTileReplaceFile(dir, oldname, newname string) error {
	// Unlike os.Rename's MoveFileEx, Root.Rename uses
	// FILE_RENAME_REPLACE_IF_EXISTS | FILE_RENAME_POSIX_SEMANTICS on Windows.
	// These flags allow an open destination to be replaced while existing
	// readers retain its old contents and new readers open the new envelope.
	// The Go implementation also handles filesystems without this capability;
	// its compatibility fallback may still fail when a destination is open.
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	return root.Rename(oldname, newname)
}
