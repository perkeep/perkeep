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

	"io"
	"log"
	"os"
)

type tempBlob struct {
}

func (tb *tempBlob) Write(d []byte) (int, os.Error) {
	return len(d), nil
}

func (mi *Indexer) ReceiveBlob(
	blobRef *blobref.BlobRef, source io.Reader, mirrorPartions []blobserver.Partition) (retsb *blobref.SizedBlobRef, err os.Error) {
	temp := new(tempBlob)
	hash := blobRef.Hash()
	var written int64
	written, err = io.Copy(io.MultiWriter(hash, temp), source)
	if err != nil {
		return
	}

	if !blobRef.HashMatches(hash) {
		err = blobserver.CorruptBlobError
		return
	}

	temp = temp
	log.Printf("Read %d bytes", written)

	// TODO: index
	return nil, os.NewError("ReceiveBlob not yet implemented by the MySQL indexer")
}

