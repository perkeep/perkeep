/*
Copyright 2026 The Perkeep Authors

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
	"slices"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/health"
)

// layoutCheckTimeout bounds how long checkLayout may take.
const layoutCheckTimeout = time.Minute

// blobpackedDirs are the directories that the high-level config puts a
// cloud storage's blobs in when it uses blobpacked: loose blobs in the
// first, and packed ones in the second.
var blobpackedDirs = []string{"loose/", "packed/"}

// startLayoutCheck runs checkLayout in the background, reporting its
// result to check.
func (sto *s3Storage) startLayoutCheck(check *health.Check) {
	check.Start()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), layoutCheckTimeout)
		defer cancel()
		problem, err := sto.checkLayout(ctx)
		switch {
		case err != nil:
			check.Fail(fmt.Errorf("checking the layout of %v: %w", sto, err))
		case problem != "":
			check.Fatal(fmt.Errorf("%v: %s. The server only sees some of the blobs in the bucket, so it would copy the rest again. Convert the bucket to one layout; see packRelatedExcludeTypes in the server configuration docs", sto, problem))
		default:
			check.OK()
		}
	}()
}

// checkLayout reports whether the bucket has blobs in the layout other
// than the one that sto reads, which happens when the high-level
// config's layout for a cloud storage changes, as it did in 2021 (see
// packRelatedExcludeTypes in pkg/types/serverconfig). If so, it returns
// a description of the problem.
//
// A storage at a blobpacked directory (such as "loose/") doesn't see
// loose blobs in the directory above it, and any other storage doesn't
// see blobs in blobpacked directories below it.
func (sto *s3Storage) checkLayout(ctx context.Context) (problem string, err error) {
	dir, base := path.Split(strings.TrimSuffix(sto.dirPrefix, "/"))
	if sto.dirPrefix != "" && slices.Contains(blobpackedDirs, base+"/") {
		found, err := sto.hasBlobsIn(ctx, dir)
		if err != nil || !found {
			return "", err
		}
		return fmt.Sprintf("this blobpacked storage, under %q, doesn't read the loose blobs at %q, written before 2021", sto.dirPrefix, "/"+dir), nil
	}
	for _, sub := range blobpackedDirs {
		res, err := sto.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:  &sto.bucket,
			Prefix:  aws.String(sto.dirPrefix + sub),
			MaxKeys: aws.Int32(1),
		})
		if err != nil {
			return "", err
		}
		if len(res.Contents) > 0 {
			return fmt.Sprintf("this storage, at %q, doesn't read the blobs under %q, written since 2021 by blobpacked", "/"+sto.dirPrefix, "/"+sto.dirPrefix+sub), nil
		}
	}
	return "", nil
}

// hasBlobsIn reports whether the first page of objects directly in dir
// (not in its subdirectories) has any named like blobs.
func (sto *s3Storage) hasBlobsIn(ctx context.Context, dir string) (bool, error) {
	res, err := sto.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket:    &sto.bucket,
		Prefix:    &dir,
		Delimiter: aws.String("/"),
	})
	if err != nil {
		return false, err
	}
	for _, obj := range res.Contents {
		if _, ok := blob.Parse(strings.TrimPrefix(aws.ToString(obj.Key), dir)); ok {
			return true, nil
		}
	}
	return false, nil
}
