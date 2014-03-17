/*
Copyright 2011 Google Inc.

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
	"log"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/context"
)

var _ blobserver.MaxEnumerateConfig = (*s3Storage)(nil)

func (sto *s3Storage) MaxEnumerate() int { return 1000 }

// marker returns the string lexically greater than the provided s
// with the same length as s.
func nextStr(s string) string {
	if s == "" {
		return s
	}
	b := []byte(s)
	i := len(b)
	for i > 0 {
		i--
		b[i]++
		if b[i] != 0 {
			break
		}
	}
	return string(b)
}

func (sto *s3Storage) EnumerateBlobs(ctx *context.Context, dest chan<- blob.SizedRef, after string, limit int) (err error) {
	defer close(dest)
	if faultEnumerate.FailErr(&err) {
		return
	}
	startAt := after
	if _, ok := blob.Parse(after); ok {
		startAt = nextStr(after)
	}
	objs, err := sto.s3Client.ListBucket(sto.bucket, startAt, limit)
	if err != nil {
		log.Printf("s3 ListBucket: %v", err)
		return err
	}
	for _, obj := range objs {
		if obj.Key == after {
			continue
		}
		br, ok := blob.Parse(obj.Key)
		if !ok {
			continue
		}
		select {
		case dest <- blob.SizedRef{Ref: br, Size: uint32(obj.Size)}:
		case <-ctx.Done():
			return context.ErrCanceled
		}
	}
	return nil
}
