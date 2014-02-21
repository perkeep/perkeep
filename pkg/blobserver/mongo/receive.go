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
	"io"
	"io/ioutil"

	"camlistore.org/pkg/blob"

	"camlistore.org/third_party/labix.org/v2/mgo"
)

const (
	// This error code is returned by mongo when a unique key violation occurs when inserting
	// See: http://www.mongodb.org/about/contributors/error-codes/
	mgoUniqueKeyErr = 11000
)

func (m *mongoStorage) ReceiveBlob(ref blob.Ref, source io.Reader) (blob.SizedRef, error) {
	blobData, err := ioutil.ReadAll(source)
	if err != nil {
		return blob.SizedRef{}, err
	}

	b := blobDoc{Key: ref.String(), Blob: blobData, Size: uint32(len(blobData))}

	if err = m.c.Insert(b); err != nil {
		// Unique key violation?
		// Then the blob already exists, no need to throw an error
		if mongoErr, isMongoErr := err.(*mgo.LastError); isMongoErr && mongoErr.Code == mgoUniqueKeyErr {
			return blob.SizedRef{Ref: ref, Size: b.Size}, nil
		}
		return blob.SizedRef{}, err
	}
	return blob.SizedRef{Ref: ref, Size: b.Size}, nil
}
