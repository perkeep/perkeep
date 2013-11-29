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

	"path/filepath"

	"camlistore.org/pkg/blob"
)

func blobFileBaseName(b blob.Ref) string {
	return fmt.Sprintf("%s-%s.dat", b.HashName(), b.Digest())
}

func (ds *DiskStorage) blobDirectory(b blob.Ref) string {
	d := b.Digest()
	if len(d) < 4 {
		d = d + "____"
	}
	return filepath.Join(ds.root, b.HashName(), d[0:2], d[2:4])
}

func (ds *DiskStorage) blobPath(b blob.Ref) string {
	return filepath.Join(ds.blobDirectory(b), blobFileBaseName(b))
}
