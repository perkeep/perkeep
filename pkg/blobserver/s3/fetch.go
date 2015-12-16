/*
Copyright 2011 Google Inc.

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

package s3

import (
	"io"

	"camlistore.org/pkg/blob"
)

func (sto *s3Storage) Fetch(blob blob.Ref) (file io.ReadCloser, size uint32, err error) {
	if faultGet.FailErr(&err) {
		return
	}
	if sto.cache != nil {
		if file, size, err = sto.cache.Fetch(blob); err == nil {
			return
		}
	}
	file, sz, err := sto.s3Client.Get(sto.bucket, sto.dirPrefix+blob.String())
	return file, uint32(sz), err
}

func (sto *s3Storage) SubFetch(br blob.Ref, offset, length int64) (rc io.ReadCloser, err error) {
	if offset < 0 || length < 0 {
		return nil, blob.ErrNegativeSubFetch
	}
	return sto.s3Client.GetPartial(sto.bucket, sto.dirPrefix+br.String(), offset, length)
}
