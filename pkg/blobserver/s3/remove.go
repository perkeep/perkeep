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
	"errors"
	"fmt"
	"slices"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"perkeep.org/pkg/blob"
)

// maxDeleteBatch is the maximum value allowed for the s3 'DeleteObjects' call
const maxDeleteBatch = 1000

func (sto *s3Storage) RemoveBlobs(ctx context.Context, blobs []blob.Ref) error {
	var errs []error
	for batch := range slices.Chunk(blobs, maxDeleteBatch) {
		objs := make([]types.ObjectIdentifier, len(batch))
		for i, br := range batch {
			objs[i].Key = aws.String(sto.dirPrefix + br.String())
		}
		res, err := sto.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: &sto.bucket,
			Delete: &types.Delete{
				Objects: objs,
				Quiet:   aws.Bool(true),
			},
		})
		if err != nil {
			return err
		}
		for _, e := range res.Errors {
			errs = append(errs, fmt.Errorf("s3: deleting %s: %s: %s", aws.ToString(e.Key), aws.ToString(e.Code), aws.ToString(e.Message)))
		}
	}
	return errors.Join(errs...)
}
