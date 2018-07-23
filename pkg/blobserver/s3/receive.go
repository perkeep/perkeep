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
	"context"
	"io"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	"go4.org/readerutil"
	"perkeep.org/pkg/blob"
)

func (sto *s3Storage) ReceiveBlob(ctx context.Context, b blob.Ref, source io.Reader) (sr blob.SizedRef, err error) {
	if faultReceive.FailErr(&err) {
		return
	}

	// unfortunately, the s3manager doesn't tell us the size of the file it uploads.
	// It's still worth using because it handles multipart uploads correctly.
	// In order to still get the size, we check if the given reader provides its
	// size, and if not count the data uploaded as we go.
	if size, ok := readerutil.Size(source); ok {
		if err := sto.doUpload(ctx, b, source); err != nil {
			return sr, err
		}
		return blob.SizedRef{Ref: b, Size: uint32(size)}, nil
	}

	cr := readerutil.CountingReader{
		Reader: source,
		N:      aws.Int64(0),
	}
	if err = sto.doUpload(ctx, b, cr); err != nil {
		return sr, err
	}
	return blob.SizedRef{Ref: b, Size: uint32(*cr.N)}, nil
}

func (sto *s3Storage) doUpload(ctx context.Context, b blob.Ref, r io.Reader) error {
	uploader := s3manager.NewUploaderWithClient(sto.client)

	_, err := uploader.UploadWithContext(ctx, &s3manager.UploadInput{
		Bucket: &sto.bucket,
		Key:    aws.String(sto.dirPrefix + b.String()),
		Body:   r,
	})
	return err
}
