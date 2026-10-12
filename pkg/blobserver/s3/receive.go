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
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"perkeep.org/pkg/blob"
)

func (sto *s3Storage) ReceiveBlob(ctx context.Context, b blob.Ref, source io.Reader) (sr blob.SizedRef, err error) {
	if faultReceive.FailErr(&err) {
		return
	}

	// Blobs are at most blobserver.MaxBlobSize, so buffer the whole
	// thing. That gives us its size and a seekable body for signing,
	// without needing multipart uploads.
	data, err := io.ReadAll(source)
	if err != nil {
		return sr, err
	}
	_, err = sto.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        &sto.bucket,
		Key:           aws.String(sto.dirPrefix + b.String()),
		Body:          bytes.NewReader(data),
		ContentLength: aws.Int64(int64(len(data))),
	})
	if err != nil {
		return sr, err
	}
	return blob.SizedRef{Ref: b, Size: uint32(len(data))}, nil
}
