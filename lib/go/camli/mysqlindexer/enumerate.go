/*
Copyright 2011 Google Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
nYou may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package mysqlindexer

import (
	"os"

	"camli/blobref"
)

func (mi *Indexer) EnumerateBlobs(dest chan<- blobref.SizedBlobRef, after string, limit uint, waitSeconds int) os.Error {
	defer close(dest)
	rs, err := mi.db.Query("SELECT blobref, size FROM blobs WHERE blobref > ? ORDER BY blobref LIMIT ?",
		after, limit)
	if err != nil {
		return err
	}
	defer rs.Close()
	return readBlobRefSizeResults(dest, rs)
}

func readBlobRefSizeResults(dest chan<- blobref.SizedBlobRef, rs ResultSet) os.Error {
	var (
		blobstr string
		size    int64
	)
	for rs.Next() {
		if err := rs.Scan(&blobstr, &size); err != nil {
			return err
		}
		br := blobref.Parse(blobstr)
		if br == nil {
			continue
		}
		dest <- blobref.SizedBlobRef{
			BlobRef: br,
			Size:    size,
		}
	}
	return nil
}
