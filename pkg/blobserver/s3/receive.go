/*
Copyright 2011 The Perkeep Authors

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
	"context"
	"crypto/md5"
	"io"

	"perkeep.org/pkg/blob"
)

func (sto *s3Storage) ReceiveBlob(ctx context.Context, b blob.Ref, source io.Reader) (sr blob.SizedRef, err error) {
	var buf bytes.Buffer
	md5h := md5.New()

	size, err := io.Copy(io.MultiWriter(&buf, md5h), source)
	if err != nil {
		return sr, err
	}

	if faultReceive.FailErr(&err) {
		return
	}

	err = sto.s3Client.PutObject(ctx, sto.dirPrefix+b.String(), sto.bucket, md5h, size, &buf)
	if err != nil {
		return sr, err
	}
	return blob.SizedRef{Ref: b, Size: uint32(size)}, nil
}
