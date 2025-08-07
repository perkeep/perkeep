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

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"perkeep.org/pkg/blob"
)

// maxDeleteBatch is the maximum value allowed for the s3 'DeleteObjects' call
const maxDeleteBatch = 1000

func (sto *s3Storage) RemoveBlobs(ctx context.Context, blobs []blob.Ref) error {
	toDelete := types.Delete{Objects: make([]types.ObjectIdentifier, 0, min(maxDeleteBatch, len(blobs)))}
	doi := s3.DeleteObjectsInput{
		Bucket: &sto.bucket,
		Delete: &toDelete,
	}
	var errs []error
	for len(blobs) != 0 {
		toDelete.Objects = toDelete.Objects[:0]
		for _, blob := range blobs[:min(maxDeleteBatch, len(blobs))] {
			toDelete.Objects = append(toDelete.Objects,
				types.ObjectIdentifier{
					Key: aws.String(sto.dirPrefix + blob.String()),
				},
			)
		}
		outputs, err := sto.client.DeleteObjects(ctx, &doi)
		if err != nil {
			return err
		}

		for _, e := range outputs.Errors {
			errs = append(errs, fmt.Errorf("%s: %s: %s", aws.ToString(e.Key), aws.ToString(e.Code), aws.ToString(e.Message)))
		}
		blobs = blobs[len(toDelete.Objects):]
	}
	return errors.Join(errs...)
}
