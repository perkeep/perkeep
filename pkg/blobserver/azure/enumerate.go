/*
Copyright 2014 The Perkeep Authors

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

package azure

import (
	"context"
	"log"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
)

var _ blobserver.MaxEnumerateConfig = (*azureStorage)(nil)

func (sto *azureStorage) MaxEnumerate() int { return 5000 }

func (sto *azureStorage) EnumerateBlobs(ctx context.Context, dest chan<- blob.SizedRef, after string, limit int) (err error) {
	defer close(dest)
	objs, err := sto.azureClient.ListBlobs(ctx, sto.container, 5000)
	if err != nil {
		log.Printf("azure ListBlobs: %v", err)
		return err
	}
	for _, obj := range objs {
		if obj.Name <= after {
			continue
		}
		br, ok := blob.Parse(obj.Name)
		if !ok {
			continue
		}
		select {
		case dest <- blob.SizedRef{Ref: br, Size: uint32(obj.Properties.ContentLength)}:
		case <-ctx.Done():
			return ctx.Err()
		}
		limit--
		if limit == 0 {
			return nil
		}
	}
	return nil
}
