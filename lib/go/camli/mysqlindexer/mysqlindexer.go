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

package mysqlindexer

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"sync"

	"camli/blobref"
	"camli/blobserver"
	"camli/jsonconfig"

	mysql "camli/third_party/github.com/Philio/GoMySQL"
)

type Indexer struct {
	*blobserver.SimpleBlobHubPartitionMap

	Host, User, Password, Database string
	Port                           int

	// TODO: does this belong at this layer?
	KeyFetcher   blobref.StreamingFetcher // for verifying claims
	OwnerBlobRef *blobref.BlobRef

	// Used for fetching blobs to find the complete sha1 of schema
	// blobs.
	BlobSource    blobserver.Storage

	clientLock    sync.Mutex
	cachedClients []*mysql.Client
}

func newFromConfig(ld blobserver.Loader, config jsonconfig.Obj) (blobserver.Storage, os.Error) {
	blobPrefix := config.RequiredString("blobSource")
	indexer := &Indexer{
		SimpleBlobHubPartitionMap: &blobserver.SimpleBlobHubPartitionMap{},
		Host:                      config.OptionalString("host", "localhost"),
		User:                      config.RequiredString("user"),
		Password:                  config.OptionalString("password", ""),
		Database:                  config.RequiredString("database"),
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}

	sto, err := ld.GetStorage(blobPrefix)
	if err != nil {
		return nil, err
	}
	indexer.BlobSource = sto

		//ownerBlobRef = client.SignerPublicKeyBlobref()
		//if ownerBlobRef == nil {
		//	log.Fatalf("Public key not configured.")
		//}

	//KeyFetcher: blobref.NewSerialStreamingFetcher(
	//			blobref.NewConfigDirFetcher(),
	//			storage),
	//}

	ok, err := indexer.IsAlive()
	if !ok {
		return nil, fmt.Errorf("Failed to connect to MySQL: %v", err)
	}

	version, err := indexer.SchemaVersion()
	if err != nil {
		return nil, fmt.Errorf("error getting schema version (need to init database?): %v", err)
	}
	if version != requiredSchemaVersion {
		if os.Getenv("CAMLI_ADVERTISED_PASSWORD") != "" {
			// Good signal that we're using the dev-server script, so help out
			// the user with a more useful tip:
			return nil, fmt.Errorf("database schema version is %d; expect %d (run \"./dev-server --wipe\" to wipe both your blobs and re-populate the database schema)", version, requiredSchemaVersion)
		}
		return nil, fmt.Errorf("database schema version is %d; expect %d (need to re-init/upgrade database?)",
			version, requiredSchemaVersion)
	}

	return indexer, nil
}

func init() {
	blobserver.RegisterStorageConstructor("mysqlindexer", blobserver.StorageConstructor(newFromConfig))
}

func (mi *Indexer) IsAlive() (ok bool, err os.Error) {
	var client *mysql.Client
	client, err = mi.getConnection()
	if err != nil {
		return
	}
	defer mi.releaseConnection(client)

	err = client.Query("SELECT 1 + 1")
	if err != nil {
		return
	}
	_, err = client.UseResult()
	if err != nil {
		return
	}
	client.FreeResult()
	return true, nil
}

func (mi *Indexer) SchemaVersion() (version int, err os.Error) {
	var client *mysql.Client
	client, err = mi.getConnection()
	if err != nil {
		return
	}
	defer mi.releaseConnection(client)

	err = client.Query("SELECT value FROM meta WHERE metakey='version'")
	if err != nil {
		return
	}
	res, err := client.UseResult()
	if err != nil {
		return
	}

	row := res.FetchRow()
	if row == nil {
		return 0, nil
	}

	version, err = strconv.Atoi(row[0].(string))

	client.FreeResult()
	return
}

// Get a free cached connection or allocate a new one.
func (mi *Indexer) getConnection() (client *mysql.Client, err os.Error) {
	mi.clientLock.Lock()
	if len(mi.cachedClients) > 0 {
		defer mi.clientLock.Unlock()
		client = mi.cachedClients[len(mi.cachedClients)-1]
		mi.cachedClients = mi.cachedClients[:len(mi.cachedClients)-1]
		// TODO: Outside the mutex, double check that the client is still good.
		return
	}
	mi.clientLock.Unlock()

	client, err = mysql.DialTCP(mi.Host, mi.User, mi.Password, mi.Database)
	return
}

// Release a client to the cached client pool.
func (mi *Indexer) releaseConnection(client *mysql.Client) {
	mi.clientLock.Lock()
	defer mi.clientLock.Unlock()
	mi.cachedClients = append(mi.cachedClients, client)
}

func (mi *Indexer) Fetch(blob *blobref.BlobRef) (blobref.ReadSeekCloser, int64, os.Error) {
	return nil, 0, os.NewError("Fetch isn't supported by the MySQL indexer")
}

func (mi *Indexer) FetchStreaming(blob *blobref.BlobRef) (io.ReadCloser, int64, os.Error) {
	return nil, 0, os.NewError("Fetch isn't supported by the MySQL indexer")
}

func (mi *Indexer) Remove(blobs []*blobref.BlobRef) os.Error {
	return os.NewError("Remove isn't supported by the MySQL indexer")
}
