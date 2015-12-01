/*
Copyright 2014 The Camlistore Authors

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
Package mongo registers the "mongo" blobserver storage type, storing
blobs using MongoDB.

Sample (low-level) config:
"/bs/": {
    "handler": "storage-mongo",
    "handlerArgs": {
       "host": "172.17.0.2",
       "database": "camlitest"
     }
},

Possible parameters:
host (optional, defaults to localhost)
database (required)
collection (optional, defaults to blobs)
user (optional)
password (optional)
*/
package mongo

import (
	"camlistore.org/pkg/blobserver"
	"camlistore.org/third_party/labix.org/v2/mgo"
	"go4.org/jsonconfig"
)

type mongoStorage struct {
	c *mgo.Collection
}

// blobDoc is the document that gets inserted in the MongoDB database
// Its fields are exported because they need to be for the mgo driver to pick them up
type blobDoc struct {
	// Key contains the string representation of a blob reference (e.g. sha1-200d278aa6dd347f494407385ceab316440d5fba).
	Key string
	// Size contains the total size of a blob.
	Size uint32
	// Blob contains the raw blob data of the blob the above Key refers to.
	Blob []byte
}

func init() {
	blobserver.RegisterStorageConstructor("mongo", blobserver.StorageConstructor(newFromConfig))
}

func newFromConfig(_ blobserver.Loader, config jsonconfig.Obj) (blobserver.Storage, error) {
	cfg, err := configFromJSON(config)
	if err != nil {
		return nil, err
	}
	return newMongoStorage(cfg)
}

var uniqueKeyIndex = mgo.Index{
	Key:        []string{"key"},
	Unique:     true,
	DropDups:   false,
	Background: false,
	Sparse:     false,
}

func newMongoStorage(cfg config) (blobserver.Storage, error) {
	session, err := getConnection(cfg.url())
	if err != nil {
		return nil, err
	}
	c := session.DB(cfg.database).C(cfg.collection)
	err = c.EnsureIndex(uniqueKeyIndex)
	if err != nil {
		return nil, err
	}
	return blobserver.Storage(&mongoStorage{c: c}), nil

}

// Config holds the parameters used to connect to MongoDB.
type config struct {
	server     string // Required. Defaults to "localhost" in ConfigFromJSON.
	database   string // Required.
	collection string // Required. Defaults to "blobs" in ConfigFromJSON.
	user       string // Optional, unless the server was configured with auth on.
	password   string // Optional, unless the server was configured with auth on.
}

func (cfg *config) url() string {
	if cfg.user == "" || cfg.password == "" {
		return cfg.server
	}
	return cfg.user + ":" + cfg.password + "@" + cfg.server + "/" + cfg.database
}

// ConfigFromJSON populates Config from cfg, and validates
// cfg. It returns an error if cfg fails to validate.
func configFromJSON(cfg jsonconfig.Obj) (config, error) {
	conf := config{
		server:     cfg.OptionalString("host", "localhost"),
		database:   cfg.RequiredString("database"),
		collection: cfg.OptionalString("collection", "blobs"),
		user:       cfg.OptionalString("user", ""),
		password:   cfg.OptionalString("password", ""),
	}
	if err := cfg.Validate(); err != nil {
		return config{}, err
	}
	return conf, nil
}

func getConnection(url string) (*mgo.Session, error) {
	session, err := mgo.Dial(url)
	if err != nil {
		return nil, err
	}
	session.SetMode(mgo.Monotonic, true)
	session.SetSafe(&mgo.Safe{}) // so we get an ErrNotFound error when deleting an absent key
	return session, nil
}
