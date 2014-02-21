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

package dockertest

import (
	"log"
	"os/exec"
	"testing"
	"time"

	"camlistore.org/pkg/netutil"
)

const mongoImage = "robinvdvleuten/mongo"

// SetupMongoContainer sets up a real MongoDB instance for testing purposes, using a Docker container.
// Currently using https://index.docker.io/u/robinvdvleuten/mongo/
func SetupMongoContainer(t *testing.T) (containerID, ip string) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("skipping without docker available in path")
	}
	if ok, err := HaveImage(mongoImage); !ok || err != nil {
		if err != nil {
			t.Skipf("Error running docker to check for %s: %v", mongoImage, err)
		}
		log.Printf("Pulling docker image %s ...", mongoImage)
		if err := Pull(mongoImage); err != nil {
			t.Skipf("Error pulling %s: %v", mongoImage, err)
		}
	}

	var err error
	containerID, err = Run("-d", mongoImage, "--smallfiles")
	if err != nil {
		t.Fatalf("docker run: %v", err)
	}

	ip, err = IP(containerID)
	if err != nil {
		t.Fatalf("Error getting container IP: %v", err)
	}

	if err := netutil.AwaitReachable(ip+":27017", 20*time.Second); err != nil {
		t.Fatal("timeout waiting for port to become reachable")
	}
	return
}
