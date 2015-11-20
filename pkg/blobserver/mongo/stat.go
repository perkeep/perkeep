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
	"fmt"

	"camlistore.org/pkg/blob"

	"go4.org/syncutil"

	"camlistore.org/third_party/labix.org/v2/mgo/bson"
)

var statGate = syncutil.NewGate(50) // arbitrary

func (m *mongoStorage) StatBlobs(dest chan<- blob.SizedRef, blobs []blob.Ref) error {
	var wg syncutil.Group

	for _, b := range blobs {
		b := b
		statGate.Start()
		wg.Go(func() error {
			defer statGate.Done()
			var doc blobDoc
			if err := m.c.Find(bson.M{"key": b.String()}).Select(bson.M{"size": 1}).One(&doc); err != nil {
				return fmt.Errorf("error statting %v: %v", b, err)
			}
			dest <- blob.SizedRef{Ref: b, Size: doc.Size}
			return nil
		})
	}
	return wg.Err()
}
