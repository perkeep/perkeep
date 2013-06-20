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

/*
#fileembed pattern .+\.(ico)$
*/
package server

import (
	"os"
	"path/filepath"

	"camlistore.org/pkg/fileembed"
)

var Files = &fileembed.Files{}

func init() {
	if root := os.Getenv("CAMLI_DEV_CAMLI_ROOT"); root != "" {
		Files.DirFallback = filepath.Join(root, filepath.FromSlash("pkg/server"))
	}
}
