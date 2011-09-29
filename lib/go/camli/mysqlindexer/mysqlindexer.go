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

	"camli/blobref"
	"camli/blobserver"
	"camli/jsonconfig"
)

type Indexer struct {
	*blobserver.SimpleBlobHubPartitionMap

	KeyFetcher blobref.StreamingFetcher // for verifying claims

	// Used for fetching blobs to find the complete sha1 of schema
	// blobs.
	BlobSource blobserver.Storage

	db *MySQLWrapper
}

func newFromConfig(ld blobserver.Loader, config jsonconfig.Obj) (blobserver.Storage, os.Error) {
	blobPrefix := config.RequiredString("blobSource")
	db := &MySQLWrapper{
		Host:     config.OptionalString("host", "localhost"),
		User:     config.RequiredString("user"),
		Password: config.OptionalString("password", ""),
		Database: config.RequiredString("database"),
	}
	indexer := &Indexer{
		SimpleBlobHubPartitionMap: &blobserver.SimpleBlobHubPartitionMap{},
		db:                        db,
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}

	sto, err := ld.GetStorage(blobPrefix)
	if err != nil {
		return nil, err
	}
	indexer.BlobSource = sto

	// Good enough, for now:
	indexer.KeyFetcher = indexer.BlobSource

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
	err = mi.db.Ping()
	ok = err == nil
	return
}

func (mi *Indexer) SchemaVersion() (version int, err os.Error) {
	rs, err := mi.db.Query("SELECT value FROM meta WHERE metakey='version'")
	if err != nil {
		return
	}
	defer rs.Close()
	if !rs.Next() {
		return 0, nil
	}
	strVersion := ""
	if err = rs.Scan(&strVersion); err != nil {
		return
	}
	return strconv.Atoi(strVersion)
}

func (mi *Indexer) Fetch(blob *blobref.BlobRef) (blobref.ReadSeekCloser, int64, os.Error) {
	return nil, 0, os.NewError("Fetch isn't supported by the MySQL indexer")
}

func (mi *Indexer) FetchStreaming(blob *blobref.BlobRef) (io.ReadCloser, int64, os.Error) {
	return nil, 0, os.NewError("Fetch isn't supported by the MySQL indexer")
}

func (mi *Indexer) RemoveBlobs(blobs []*blobref.BlobRef) os.Error {
	return os.NewError("RemoveBlobs isn't supported by the MySQL indexer")
}
