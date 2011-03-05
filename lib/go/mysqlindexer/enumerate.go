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
)

func (mi *Indexer) EnumerateBlobs(dest chan *blobref.SizedBlobRef,
	partition blobserver.Partition,
	after string,
	limit uint,
	waitSeconds int) os.Error {
	// TODO: finish
	return os.NewError("Fetch isn't supported by the MySQL indexer")
}

