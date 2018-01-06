/*
Copyright 2014 The Perkeep Authors

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

package mongo

import (
	"context"
	"fmt"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"

	"go4.org/syncutil"
	"gopkg.in/mgo.v2/bson"
)

var statGate = syncutil.NewGate(50) // arbitrary

func (m *mongoStorage) StatBlobs(ctx context.Context, blobs []blob.Ref, fn func(blob.SizedRef) error) error {
	return blobserver.StatBlobsParallelHelper(ctx, blobs, fn, statGate, func(b blob.Ref) (sb blob.SizedRef, err error) {
		var doc blobDoc
		if err := m.c.Find(bson.M{"key": b.String()}).Select(bson.M{"size": 1}).One(&doc); err != nil {
			// TODO: deal with mgo.ErrNotFound or *mgo.QueryError.Code not found?
			return sb, fmt.Errorf("mongo: error statting %v: %v", b, err)
		}
		return blob.SizedRef{Ref: b, Size: doc.Size}, nil
	})
}
