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

/*
Package files implements the blobserver interface by storing each blob
in its own file in nested directories. Users don't use the "files"
type directly; it's used by "localdisk" and in the future "sftp" and
"webdav".
*/
package files // import "perkeep.org/pkg/blobserver/files"
import (
	"io"
	"os"
)

// VFS describes the virtual filesystem needed by this package.
//
// It is currently not possible to use this from other packages, but
// that will change.
type VFS interface {
	Remove(string) error // files, not directories
	RemoveDir(string) error
	Stat(string) (os.FileInfo, error)
	Lstat(string) (os.FileInfo, error)
	Open(string) (ReadableFile, error)
	MkdirAll(path string, perm os.FileMode) error

	// Rename is a POSIX-style rename, overwriting newname if it exists.
	Rename(oldname, newname string) error

	// TempFile should behave like io/ioutil.TempFile.
	TempFile(dir, prefix string) (WritableFile, error)

	ReadDirNames(dir string) ([]string, error)
}

// WritableFile is the interface required by files opened for Write
// from VFS.TempFile.
type WritableFile interface {
	io.Writer
	io.Closer
	// Name returns the full path to the file.
	// It should behave like (*os.File).Name.
	Name() string
	// Sync fsyncs the file, like (*os.File).Sync.
	Sync() error
}

// ReadableFile is the interface required by read-only files
// returned by VFS.Open.
type ReadableFile interface {
	io.Reader
	io.Seeker
	io.Closer
}
