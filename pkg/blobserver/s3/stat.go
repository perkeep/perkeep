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

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/s3"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"

	"go4.org/syncutil"
)

var statGate = syncutil.NewGate(20) // arbitrary

func (sto *s3Storage) StatBlobs(ctx context.Context, blobs []blob.Ref, fn func(blob.SizedRef) error) (err error) {
	if faultStat.FailErr(&err) {
		return
	}

	return blobserver.StatBlobsParallelHelper(ctx, blobs, fn, statGate, func(br blob.Ref) (sb blob.SizedRef, err error) {
		resp, err := sto.client.HeadObjectWithContext(ctx, &s3.HeadObjectInput{
			Bucket: &sto.bucket,
			Key:    aws.String(sto.dirPrefix + br.String()),
		})
		if err == nil {
			return blob.SizedRef{Ref: br, Size: uint32(*resp.ContentLength)}, nil
		}
		if isNotFound(err) {
			return sb, nil
		}
		return sb, fmt.Errorf("error statting %v: %v", br, err)
	})
}
