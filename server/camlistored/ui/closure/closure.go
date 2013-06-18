/*
Copyright 2013 Google Inc.

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

package closure

import (
	"archive/zip"
	"errors"
	"os"
)

// ZipData is either the empty string (when compiling with "go get",
// or the dev-server), or is initialized to a base64-encoded zip file
// of the Closure library (when using make.go, which puts an extra
// file in this package containing an init function to set ZipData).
var ZipData string

func Zip() (*zip.Reader, error) {
	if ZipData == "" {
		return nil, os.ErrNotExist
	}
	return nil, errors.New("TODO: implement")
}
