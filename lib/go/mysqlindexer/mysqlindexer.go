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
	"camli/blobref"
	"camli/blobserver"

	"os"
	"sync"

	mysql "github.com/Philio/GoMySQL"
)

type Indexer struct {
	Host, User, Password, Database string
	Port                           int

	hubLock sync.Mutex
	hubMap  map[blobserver.Partition]blobserver.BlobHub

	clientLock    sync.Mutex
	cachedClients []*mysql.Client
}

func (mi *Indexer) IsAlive() (ok bool, err os.Error) {
	var client *mysql.Client
	client, err = mi.getConnection()
	defer mi.releaseConnection(client)

	if err != nil {
		return
	}
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

func (mi *Indexer) GetBlobHub(partition blobserver.Partition) blobserver.BlobHub {
	mi.hubLock.Lock()
	defer mi.hubLock.Unlock()
	if hub, ok := mi.hubMap[partition]; ok {
		return hub
	}

	// TODO: in the future, allow for different blob hub
	// implementations rather than the
	// everything-in-memory-on-a-single-machine SimpleBlobHub.
	hub := new(blobserver.SimpleBlobHub)
	mi.hubMap[partition] = hub
	return hub
}

func (mi *Indexer) Fetch(blob *blobref.BlobRef) (blobref.ReadSeekCloser, int64, os.Error) {
	return nil, 0, os.NewError("Fetch isn't supported by the MySQL indexer")
}

func (mi *Indexer) Remove(partition blobserver.Partition, blobs []*blobref.BlobRef) os.Error {
	return os.NewError("Remove isn't supported by the MySQL indexer")
}
