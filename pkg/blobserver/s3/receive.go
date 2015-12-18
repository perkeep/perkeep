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
	"bytes"
	"crypto/md5"
	"io"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
)

func (sto *s3Storage) ReceiveBlob(b blob.Ref, source io.Reader) (sr blob.SizedRef, err error) {
	var buf bytes.Buffer
	md5h := md5.New()

	size, err := io.Copy(io.MultiWriter(&buf, md5h), source)
	if err != nil {
		return sr, err
	}

	if faultReceive.FailErr(&err) {
		return
	}

	err = sto.s3Client.PutObject(sto.dirPrefix+b.String(), sto.bucket, md5h, size, &buf)
	if err != nil {
		return sr, err
	}
	if sto.cache != nil {
		// NoHash because it's already verified if we read it
		// without errors on the io.Copy above.
		blobserver.ReceiveNoHash(sto.cache, b, bytes.NewReader(buf.Bytes()))
	}
	return blob.SizedRef{Ref: b, Size: uint32(size)}, nil
}
