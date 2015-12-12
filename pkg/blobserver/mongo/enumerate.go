/*
Copyright 2014 The Camlistore Authors

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
	"log"

	"camlistore.org/pkg/blob"
	"camlistore.org/third_party/labix.org/v2/mgo/bson"
	"golang.org/x/net/context"
)

func (m *mongoStorage) EnumerateBlobs(ctx context.Context, dest chan<- blob.SizedRef, after string, limit int) error {
	defer close(dest)

	var b blobDoc
	var qry bson.M
	if after != "" {
		qry = bson.M{"key": bson.M{"$gt": after}}
	}
	iter := m.c.Find(qry).Limit(limit).Select(bson.M{"key": 1, "size": 1}).Sort("key").Iter()

	for iter.Next(&b) {
		br, ok := blob.Parse(b.Key)
		if !ok {
			continue
		}
		select {
		case dest <- blob.SizedRef{Ref: br, Size: uint32(b.Size)}:
		case <-ctx.Done():
			// Close the iterator but ignore the error value since we are already cancelling
			if err := iter.Close(); err != nil {
				log.Printf("Error closing iterator after enumerating: %v", err)
			}
			return ctx.Err()
		}
	}

	if err := iter.Close(); err != nil {
		return err
	}

	return nil
}
