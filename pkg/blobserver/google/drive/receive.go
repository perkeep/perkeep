/*
Copyright 2013 Google Inc.

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

package drive

import (
	"io"

	"camlistore.org/pkg/blob"
)

func (sto *driveStorage) ReceiveBlob(b blob.Ref, source io.Reader) (blob.SizedRef, error) {
	file, err := sto.service.Upsert(b.String(), source)
	if err != nil {
		return blob.SizedRef{Ref: b, Size: 0}, err
	}
	return blob.SizedRef{Ref: b, Size: uint32(file.FileSize)}, err
}
