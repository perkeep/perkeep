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

package s3

import (
	"log"
	"os"

	"camli/blobref"
	"camli/blobserver"
)

var _ = log.Printf

func (sto *s3Storage) MaxEnumerate() uint { return 1000 }

func (sto *s3Storage) EnumerateBlobs(dest chan *blobref.SizedBlobRef, partition blobserver.Partition, after string, limit uint, waitSeconds int) os.Error {
	defer close(dest)
	objs, err := sto.s3Client.ListBucket(sto.bucket, after, limit)
	if err != nil {
		log.Printf("s3 ListBucket: %v", err)
		return err
	}
	for _, obj := range objs {
		br := blobref.Parse(obj.Key)
		if br == nil {
			continue
		}
		dest <- &blobref.SizedBlobRef{BlobRef: br, Size: obj.Size}
	}
	return nil
}
