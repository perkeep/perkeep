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
	"camli/blobserver"
	"os"
)

type s3Storage struct {
	*blobserver.SimpleBlobHubPartitionMap
	*blobserver.NoImplStorage
}

func New(bucketPrefixURL, accessKey, secretAccessKey string) (storage blobserver.Storage, err os.Error) {
	return &s3Storage{
		&blobserver.SimpleBlobHubPartitionMap{},
		&blobserver.NoImplStorage{},
	}, nil
}

func newFromConfig(config map[string]interface{}) (storage blobserver.Storage, err os.Error) {
	// TODO: implement
	return nil, os.NewError("not implemented")
}

func init() {
	blobserver.RegisterStorageConstructor("filesystem", blobserver.StorageConstructor(newFromConfig))
}
