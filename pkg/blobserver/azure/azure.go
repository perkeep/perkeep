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

/*
Package azure registers the "azure" blobserver storage type, storing
blobs in a Microsoft Azure Blob Storage container.

Example low-level config:

	"/r1/": {
	    "handler": "storage-azure",
	    "handlerArgs": {
	       "container": "foo",
	       "azure_account": "...",
	       "azure_access_key": "...",
	       "skipStartupCheck": false
	     }
	},
*/
package azure

import (
	"context"
	"encoding/base64"
	"fmt"

	"go4.org/jsonconfig"
	"perkeep.org/internal/azure/storage"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/blobserver/memory"
	"perkeep.org/pkg/constants"
)

var (
	_ blob.SubFetcher               = (*azureStorage)(nil)
	_ blobserver.MaxEnumerateConfig = (*azureStorage)(nil)
)

type azureStorage struct {
	azureClient *storage.Client
	container   string
	hostname    string
	cache       *memory.Storage // or nil for no cache
}

func (sto *azureStorage) String() string {
	return fmt.Sprintf("\"azure\" blob storage at host %q, container %q", sto.hostname, sto.container)
}

func newFromConfig(_ blobserver.Loader, config jsonconfig.Obj) (blobserver.Storage, error) {
	hostname := config.OptionalString("hostname", "")
	cacheSize := config.OptionalInt64("cacheSize", 32<<20)

	AccessKey := make([]byte, 66)
	i, err := base64.StdEncoding.Decode(AccessKey, []byte(config.RequiredString("azure_access_key")))
	if err != nil {
		panic(err)
	}
	AccessKey = AccessKey[:i]
	client := &storage.Client{
		Auth: &storage.Auth{
			Account:   config.RequiredString("azure_account"),
			AccessKey: AccessKey,
		},
		Hostname: hostname,
	}
	sto := &azureStorage{
		azureClient: client,
		container:   config.RequiredString("container"),
		hostname:    hostname,
	}
	skipStartupCheck := config.OptionalBool("skipStartupCheck", false)
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if cacheSize != 0 {
		sto.cache = memory.NewCache(cacheSize)
	}
	if !skipStartupCheck {
		_, err := client.ListBlobs(context.TODO(), sto.container, 1)
		if serr, ok := err.(*storage.Error); ok {
			if serr.AzureError.Code == "ContainerNotFound" {
				return nil, fmt.Errorf("container %q doesn't exist", sto.container)
			}
			return nil, fmt.Errorf("error listing container %s: %v", sto.container, serr)
		} else if err != nil {
			return nil, fmt.Errorf("error listing container %s: %v", sto.container, err)
		}
	}
	return sto, nil
}

func init() {
	// It's assumed the MaxBlobSize won't change in the foreseeable future.
	// However, just in case it does, let's be aware that the current implementation doesn't support it.
	// Azure itself can support it by splitting up requests in multiple parts but that's more work which is not yet needed.
	if constants.MaxBlobSize > 64000000 {
		panic("Blob sizes over 64mb aren't supported by Azure")
	}
	blobserver.RegisterStorageConstructor("azure", blobserver.StorageConstructor(newFromConfig))
}
