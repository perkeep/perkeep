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
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"

	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/index"
	"camlistore.org/pkg/jsonconfig"

	_ "camlistore.org/third_party/github.com/ziutek/mymysql/godrv"
)

type myIndexStorage struct {
	host, user, password, database string

	db *sql.DB
}

var _ index.IndexStorage = (*myIndexStorage)(nil)

type batchTx struct {
	tx  *sql.Tx
	err error // sticky
}

func (b *batchTx) Set(key, value string) {
	if b.err != nil {
		return
	}
	_, b.err = b.tx.Exec("REPLACE INTO rows (k, v) VALUES (?, ?)", key, value)
}

func (b *batchTx) Delete(key string) {
	if b.err != nil {
		return
	}
	_, b.err = b.tx.Exec("DELETE FROM rows WHERE k=?", key)
}

func (ms *myIndexStorage) BeginBatch() index.BatchMutation {
	tx, err := ms.db.Begin()
	return &batchTx{
		tx:  tx,
		err: err,
	}
}

func (ms *myIndexStorage) CommitBatch(b index.BatchMutation) error {
	bt, ok := b.(*batchTx)
	if !ok {
		return fmt.Errorf("wrong BatchMutation type %T", b)
	}
	if bt.err != nil {
		return bt.err
	}
	return bt.tx.Commit()
}

func (ms *myIndexStorage) Get(key string) (value string, err error) {
	err = ms.db.QueryRow("SELECT v FROM rows WHERE k=?", key).Scan(&value)
	return
}

func (ms *myIndexStorage) Set(key, value string) error {
	_, err := ms.db.Exec("REPLACE INTO rows (k, v) VALUES (?, ?)", key, value)
	return err
}

func (ms *myIndexStorage) Delete(key string) error {
	_, err := ms.db.Exec("DELETE FROM rows WHERE k=?", key)
	return err
}

func (ms *myIndexStorage) Find(key string) index.Iterator {
	return &iter{
		ms:  ms,
		low: key,
		op:  ">=",
	}
}

// iter is a iterator over sorted key/value pairs in rows.
type iter struct {
	ms  *myIndexStorage
	low string
	op  string // ">=" initially, then ">"
	err error  // accumulated error, returned at Close

	rows *sql.Rows // if non-nil, the rows we're reading from

	batchSize int // how big our LIMIT query was
	seen      int // how many rows we've seen this query

	key   string
	value string
}

var errClosed = errors.New("mysqlindexer: Iterator already closed")

func (t *iter) Key() string   { return t.key }
func (t *iter) Value() string { return t.value }

func (t *iter) Close() error {
	if t.rows != nil {
		t.rows.Close()
	}
	err := t.err
	t.err = errClosed
	return err
}

func (t *iter) Next() bool {
	if t.err != nil {
		return false
	}
	if t.rows == nil {
		const batchSize = 50
		t.batchSize = batchSize
		t.rows, t.err = t.ms.db.Query(
			"SELECT k, v FROM rows WHERE k "+t.op+" ? ORDER BY k LIMIT "+strconv.Itoa(batchSize),
			t.low)
		if t.err != nil {
			log.Printf("unexpected query error: %v", t.err)
			return false
		}
		t.seen = 0
		t.op = ">"
	}
	if !t.rows.Next() {
		if t.seen == t.batchSize {
			t.rows = nil
			return t.Next()
		}
		return false
	}
	t.err = t.rows.Scan(&t.key, &t.value)
	if t.err != nil {
		log.Printf("unexpected Scan error: %v", t.err)
		return false
	}
	t.low = t.key
	t.seen++
	return true
}

func newFromConfig(ld blobserver.Loader, config jsonconfig.Obj) (blobserver.Storage, error) {
	blobPrefix := config.RequiredString("blobSource")
	is := &myIndexStorage{
		host:     config.OptionalString("host", "localhost"),
		user:     config.RequiredString("user"),
		password: config.OptionalString("password", ""),
		database: config.RequiredString("database"),
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	sto, err := ld.GetStorage(blobPrefix)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("mymysql", is.database+"/"+is.user+"/"+is.password)
	if err != nil {
		return nil, err
	}
	is.db = db
	if err := is.ping(); err != nil {
		return nil, err
	}

	version, err := is.SchemaVersion()
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

	ix := index.New(is)
	ix.BlobSource = sto
	// Good enough, for now:
	ix.KeyFetcher = ix.BlobSource

	return ix, nil
}

func init() {
	blobserver.RegisterStorageConstructor("mysqlindexer", blobserver.StorageConstructor(newFromConfig))
}

func (mi *myIndexStorage) ping() error {
	// TODO(bradfitz): something more efficient here?
	_, err := mi.SchemaVersion()
	return err
}

func (mi *myIndexStorage) SchemaVersion() (version int, err error) {
	err = mi.db.QueryRow("SELECT value FROM meta WHERE metakey='version'").Scan(&version)
	return
}
