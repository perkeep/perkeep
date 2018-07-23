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
	"fmt"
	"io"
	"os"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/s3"
	"perkeep.org/pkg/blob"
)

func (sto *s3Storage) Fetch(ctx context.Context, blob blob.Ref) (file io.ReadCloser, size uint32, err error) {
	if faultGet.FailErr(&err) {
		return
	}
	return sto.fetch(ctx, blob, nil)
}

func (sto *s3Storage) SubFetch(ctx context.Context, br blob.Ref, offset, length int64) (rc io.ReadCloser, err error) {
	if offset < 0 || length < 0 {
		return nil, blob.ErrNegativeSubFetch
	}
	rc, _, err = sto.fetch(ctx, br, aws.String(fmt.Sprintf("bytes=%d-%d", offset, offset+length-1)))
	return
}

func (sto *s3Storage) fetch(ctx context.Context, br blob.Ref, objRange *string) (rc io.ReadCloser, size uint32, err error) {
	resp, err := sto.client.GetObjectWithContext(ctx, &s3.GetObjectInput{
		Bucket: &sto.bucket,
		Key:    aws.String(sto.dirPrefix + br.String()),
		Range:  objRange,
	})
	if err == nil {
		return resp.Body, uint32(*resp.ContentLength), err
	}
	if resp.Body != nil {
		resp.Body.Close()
	}
	if isNotFound(err) {
		return nil, 0, os.ErrNotExist
	}
	if aerr, ok := err.(awserr.Error); ok {
		if aerr.Code() == "InvalidRange" {
			return nil, 0, blob.ErrOutOfRangeOffsetSubFetch
		}
	}
	return nil, 0, err
}
