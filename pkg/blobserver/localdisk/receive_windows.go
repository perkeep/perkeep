/*
Copyright 2011 Google Inc.

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
	"os"
	"syscall"
)

// mapRenameError returns nil if and only if
// 1) the input err is the error returned on windows when trying to rename
// a file over one that already exists
// 2) oldfile and newfile are the same files (i.e have the same size)
func mapRenameError(err error, oldfile, newfile string) error {
	linkErr, ok := err.(*os.LinkError)
	if !ok {
		return err
	}
	if linkErr.Err != error(syscall.ERROR_ALREADY_EXISTS) {
		return err
	}
	// TODO(mpl): actually on linux at least, os.Rename apparently
	// erases the destination with no error even if it is different
	// from the source.
	// So why don't we allow the same for windows? and if needed,
	// do the check size before renaming?
	statNew, err := os.Stat(newfile)
	if err != nil {
		return err
	}
	statOld, err := os.Stat(oldfile)
	if err != nil {
		return err
	}
	if statNew.Size() != statOld.Size() {
		return fmt.Errorf("Will not overwrite destination file %v with source file %v, as they are different.", newfile, oldfile)
	}
	return nil
}
