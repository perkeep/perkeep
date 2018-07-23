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
	"path"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/s3"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
)

// s3MaxKeys is the maximum value the S3 API documentation claims is supported
// for the 'MaxKeys' field
const s3MaxKeys = 1000

var _ blobserver.MaxEnumerateConfig = (*s3Storage)(nil)

func (sto *s3Storage) MaxEnumerate() int { return 1000 }

func (sto *s3Storage) EnumerateBlobs(ctx context.Context, dest chan<- blob.SizedRef, after string, limit int) (retErr error) {
	defer close(dest)
	if faultEnumerate.FailErr(&retErr) {
		return
	}

	var maxKeys *int64
	if limit < s3MaxKeys {
		maxKeys = aws.Int64(int64(limit))
	}

	keysGotten := 0

	err := sto.client.ListObjectsV2PagesWithContext(ctx, &s3.ListObjectsV2Input{
		Bucket:     &sto.bucket,
		StartAfter: aws.String(sto.dirPrefix + after),
		MaxKeys:    maxKeys,
	}, func(page *s3.ListObjectsV2Output, lastPage bool) bool {
		for _, obj := range page.Contents {
			dir, file := path.Split(*obj.Key)
			if dir != sto.dirPrefix {
				continue
			}
			if file == after {
				continue
			}
			br, ok := blob.Parse(file)
			if !ok {
				retErr = fmt.Errorf("non-Perkeep object named %q found in %v s3 bucket", file, sto.bucket)
				return false
			}
			select {
			case dest <- blob.SizedRef{Ref: br, Size: uint32(*obj.Size)}:
			case <-ctx.Done():
				retErr = ctx.Err()
				return false
			}
			keysGotten++
			if keysGotten >= limit {
				return false
			}
		}
		return true
	})
	if err == nil {
		err = retErr
	}

	if err != nil {
		return fmt.Errorf("s3 EnumerateBlobs: %v", err)
	}
	return nil
}
