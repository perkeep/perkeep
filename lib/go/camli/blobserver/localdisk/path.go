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
	"camli/blobref"
	"camli/blobserver"

	"fmt"
	"http"
)

func BlobFileBaseName(b *blobref.BlobRef) string {
	return fmt.Sprintf("%s-%s.dat", b.HashName(), b.Digest())
}

func (ds *DiskStorage) blobDirectory(partition blobserver.NamedPartition, b *blobref.BlobRef) string {
	d := b.Digest()
	if len(d) < 6 {
		d = d + "______"
	}
	return fmt.Sprintf("%s/%s/%s/%s", ds.PartitionRoot(partition), b.HashName(), d[0:3], d[3:6])
}

func (ds *DiskStorage) blobPath(partition blobserver.NamedPartition, b *blobref.BlobRef) string {
	return fmt.Sprintf("%s/%s", ds.blobDirectory(partition, b), BlobFileBaseName(b))
}

func (ds *DiskStorage) PartitionRoot(partition blobserver.NamedPartition) string {
	if partition == nil {
		return ds.root
	}
	if pname := partition.Name(); pname != "" {
		return fmt.Sprintf("%s/partition/%s", ds.root, http.URLEscape(pname))
	}
	return ds.root
}
