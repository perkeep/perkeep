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
)

func BlobFileBaseName(b *blobref.BlobRef) string {
	return fmt.Sprintf("%s-%s.dat", b.HashName(), b.Digest())
}

func (ds *diskStorage) blobPartitionDirName(partitionDirSlash string, b *blobref.BlobRef) string {
	d := b.Digest()
	if len(d) < 6 {
		d = d + "______"
	}
	return fmt.Sprintf("%s/%s%s/%s/%s",
		ds.root, partitionDirSlash,
		b.HashName(), d[0:3], d[3:6])
}

func (ds *diskStorage) blobDirectoryName(b *blobref.BlobRef) string {
	return ds.blobPartitionDirName("", b)
}

func (ds *diskStorage) blobFileName(b *blobref.BlobRef) string {
	return fmt.Sprintf("%s/%s-%s.dat", ds.blobDirectoryName(b), b.HashName(), b.Digest())
}

func (ds *diskStorage) blobPartitionDirectoryName(partition blobserver.Partition, b *blobref.BlobRef) string {
	return ds.blobPartitionDirName("partition/"+string(partition)+"/", b)
}

func (ds *diskStorage) partitionBlobFileName(partition blobserver.Partition, b *blobref.BlobRef) string {
	return fmt.Sprintf("%s/%s-%s.dat", ds.blobPartitionDirectoryName(partition, b), b.HashName(), b.Digest())
}
