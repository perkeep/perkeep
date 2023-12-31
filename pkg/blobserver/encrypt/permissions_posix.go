//go:build !windows

/*
Copyright 2021 The Perkeep Authors

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

package encrypt

import (
	"fmt"
	"os"
)

func checkKeyFilePermissions(keyFile string) error {
	if fileInfo, err := os.Stat(keyFile); err != nil {
		return fmt.Errorf("Checking for key file permissions %v: %v", keyFile, err)
	} else if fileInfo.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("Key file permissions are too permissive (%o), they should be 600", fileInfo.Mode().Perm())
	}
	return nil
}
