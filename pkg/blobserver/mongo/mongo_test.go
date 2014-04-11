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

package mongo

import (
	"testing"

	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/blobserver/storagetest"
	"camlistore.org/pkg/test/dockertest"
)

// TestMongoStorage tests against a real MongoDB instance, using a Docker container.
// Currently using https://index.docker.io/u/robinvdvleuten/mongo/
func TestMongoStorage(t *testing.T) {
	// SetupMongoContainer may skip or fatal the test if docker isn't found or something goes wrong when setting up the container.
	// Thus, no error is returned
	containerID, ip := dockertest.SetupMongoContainer(t)
	defer containerID.KillRemove(t)

	sto, err := newMongoStorage(config{
		server:     ip,
		database:   "camlitest",
		collection: "blobs",
	})
	if err != nil {
		t.Fatalf("mongo.NewMongoStorage = %v", err)
	}

	storagetest.Test(t, func(t *testing.T) (blobserver.Storage, func()) {
		return sto, func() {}
	})
}
