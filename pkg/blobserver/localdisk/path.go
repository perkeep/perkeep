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

	"camlistore.org/pkg/blobref"
	"net/url"
)

func blobFileBaseName(b *blobref.BlobRef) string {
	return fmt.Sprintf("%s-%s.dat", b.HashName(), b.Digest())
}

func (ds *DiskStorage) blobDirectory(partition string, b *blobref.BlobRef) string {
	d := b.Digest()
	if len(d) < 6 {
		d = d + "______"
	}
	return filepath.Join(ds.PartitionRoot(partition), b.HashName(), d[0:3], d[3:6])
}

func (ds *DiskStorage) blobPath(partition string, b *blobref.BlobRef) string {
	return filepath.Join(ds.blobDirectory(partition, b), blobFileBaseName(b))
}

func (ds *DiskStorage) PartitionRoot(partition string) string {
	if partition == "" {
		return ds.root
	}
	return filepath.Join(ds.root, "partition", url.QueryEscape(partition))
}
