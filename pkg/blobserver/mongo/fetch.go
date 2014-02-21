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
	"bytes"
	"fmt"
	"io"
	"io/ioutil"

	"camlistore.org/pkg/blob"
	"camlistore.org/third_party/labix.org/v2/mgo/bson"
)

func (m *mongoStorage) Fetch(ref blob.Ref) (io.ReadCloser, uint32, error) {
	var b blobDoc
	err := m.c.Find(bson.M{"key": ref.String()}).One(&b)
	if err != nil {
		return nil, 0, err
	}
	if len(b.Blob) != int(b.Size) {
		return nil, 0, fmt.Errorf("blob data size %d doesn't match meta size %d", len(b.Blob), b.Size)
	}
	return ioutil.NopCloser(bytes.NewReader(b.Blob)), b.Size, nil
}
