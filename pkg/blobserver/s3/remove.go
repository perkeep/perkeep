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

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	"perkeep.org/pkg/blob"
)

// maxDeleteBatch is the maximum value allowed for the s3 'DeleteObjects' call
const maxDeleteBatch = 1000

func (sto *s3Storage) RemoveBlobs(ctx context.Context, blobs []blob.Ref) error {
	toDelete := make([]s3manager.BatchDeleteObject, 0, len(blobs))
	for _, blob := range blobs {
		toDelete = append(toDelete, s3manager.BatchDeleteObject{
			Object: &s3.DeleteObjectInput{
				Bucket: &sto.bucket,
				Key:    aws.String(sto.dirPrefix + blob.String()),
			},
		})
	}

	batchDeleter := s3manager.NewBatchDeleteWithClient(sto.client)
	batchDeleter.BatchSize = maxDeleteBatch

	return batchDeleter.Delete(ctx, &s3manager.DeleteObjectsIterator{
		Objects: toDelete,
	})
}
