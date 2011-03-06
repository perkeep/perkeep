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
	"camli/blobserver"

	"os"

	mysql "github.com/Philio/GoMySQL"
)

type blobRow struct {
	blobref string
	size    int64
}

func (mi *Indexer) EnumerateBlobs(dest chan *blobref.SizedBlobRef, partition blobserver.Partition, after string, limit uint, waitSeconds int) (err os.Error) {
	var client *mysql.Client
	client, err = mi.getConnection()
	if err != nil {
		return
	}
	defer mi.releaseConnection(client)

	var stmt *mysql.Statement
	stmt, err = client.Prepare("SELECT * FROM blobs WHERE blobref > ? ORDER BY blobref LIMIT ?")
	if err != nil {
		return
	}
	err = stmt.BindParams(after, limit)
	if err != nil {
		return
	}
	err = stmt.Execute()
	if err != nil {
		return
	}

	var row blobRow
	stmt.BindResult(&row.blobref, &row.size)
	for {
		var done bool
		done, err = stmt.Fetch()
		if done {
			break
		}
		if err != nil {
			return
		}
		br := blobref.Parse(row.blobref)
		if br == nil {
			continue
		}
		dest <- &blobref.SizedBlobRef{
			BlobRef: br,
			Size:    row.size,
		}
	}
	dest <- nil
	return
}
