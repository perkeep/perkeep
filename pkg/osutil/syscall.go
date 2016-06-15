/*
Copyright 2016 The Camlistore Authors.

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

package osutil

// Mkfifo creates a FIFO file named path. It returns ErrNotSupported on
// unsupported systems.
func Mkfifo(path string, mode uint32) error {
	return mkfifo(path, mode)
}

// Mksocket creates a socket file (a Unix Domain Socket) named path. It returns
// ErrNotSupported on unsupported systems.
func Mksocket(path string) error {
	return mksocket(path)
}

// MaxFD returns the maximum number of open file descriptors allowed. It returns
// ErrNotSupported on unsupported systems.
func MaxFD() (uint64, error) {
	return maxFD()
}
