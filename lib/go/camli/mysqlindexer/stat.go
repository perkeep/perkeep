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
	"camli/blobref"

	"fmt"
	"os"
	"strings"
)

func (mi *Indexer) StatBlobs(dest chan<- blobref.SizedBlobRef, blobs []*blobref.BlobRef, waitSeconds int) os.Error {
	quotedBlobRefs := []string{}
	for _, br := range blobs {
		quotedBlobRefs = append(quotedBlobRefs, fmt.Sprintf("%q", br.String()))
	}
	sql := "SELECT blobref, size FROM blobs WHERE blobref IN (" +
		strings.Join(quotedBlobRefs, ", ") + ")"

	rs, err := mi.db.Query(sql)
	if err != nil {
		return err
	}
	defer rs.Close()
	return readBlobRefSizeResults(dest, rs)
}
