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
	"io"

	"perkeep.org/pkg/blob"
)

func (sto *azureStorage) Fetch(ctx context.Context, blob blob.Ref) (file io.ReadCloser, size uint32, err error) {
	if sto.cache != nil {
		if file, size, err = sto.cache.Fetch(ctx, blob); err == nil {
			return
		}
	}
	file, sz, err := sto.azureClient.Get(ctx, sto.container, blob.String())
	return file, uint32(sz), err
}

func (sto *azureStorage) SubFetch(ctx context.Context, br blob.Ref, offset, length int64) (rc io.ReadCloser, err error) {
	return sto.azureClient.GetPartial(ctx, sto.container, br.String(), offset, length)
}
