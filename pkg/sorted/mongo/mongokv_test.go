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
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"testing"
	"time"

	"camlistore.org/pkg/netutil"
	"camlistore.org/pkg/sorted/kvtest"
)

const mongoImage = "robinvdvleuten/mongo"

// TestMongoKV tests against a real MongoDB instance, using a Docker container.
// Currently using https://index.docker.io/u/robinvdvleuten/mongo/
func TestMongoKV(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("skipping without docker available in path")
	}
	if ok, err := dockerHaveImage(mongoImage); !ok || err != nil {
		if err != nil {
			t.Skipf("Error running docker to check for %s: %v", mongoImage, err)
		}
		log.Printf("Pulling docker image %s ...", mongoImage)
		if err := dockerPull(mongoImage); err != nil {
			t.Skipf("Error pulling %s: %v", mongoImage, err)
		}
	}
	containerID, err := dockerRun("-d", mongoImage, "--smallfiles")
	if err != nil {
		t.Fatalf("docker run: %v", err)
	}
	defer dockerKillContainer(containerID)

	ip, err := dockerIP(containerID)
	if err != nil {
		t.Fatalf("Error getting container IP: %v", err)
	}

	if err := netutil.AwaitReachable(ip+":27017", 10*time.Second); err != nil {
		t.Fatal("timeout waiting for port to become reachable")
	}

	kv, err := NewKeyValue(Config{
		Server:   ip,
		Database: "camlitest",
	})
	if err != nil {
		t.Fatalf("monogo.NewKeyValue = %v", err)
	}
	kvtest.TestSorted(t, kv)
}

// TODO(bradfitz): move all this docker stuff out into our own utility
// package. Or consider using docker directly. But
// http://godoc.org/github.com/dotcloud/docker looks like a mess of an
// API. I think it's just their internals.

func dockerHaveImage(name string) (ok bool, err error) {
	out, err := exec.Command("docker", "images", "--no-trunc").Output()
	if err != nil {
		return
	}
	return bytes.Contains(out, []byte(name)), nil
}

func dockerRun(args ...string) (containerID string, err error) {
	runOut, err := exec.Command("docker", append([]string{"run"}, args...)...).Output()
	if err != nil {
		return
	}
	containerID = strings.TrimSpace(string(runOut))
	if containerID == "" {
		return "", errors.New("unexpected empty output from `docker run`")
	}
	return
}

func dockerKillContainer(container string) error {
	return exec.Command("docker", "kill", container).Run()
}

func dockerPull(name string) error {
	out, err := exec.Command("docker", "pull", name).CombinedOutput()
	if err != nil {
		err = fmt.Errorf("%v: %s", err, out)
	}
	return err
}

func dockerIP(containerID string) (string, error) {
	out, err := exec.Command("docker", "inspect", containerID).Output()
	if err != nil {
		return "", err
	}
	type networkSettings struct {
		IPAddress string
	}
	type container struct {
		NetworkSettings networkSettings
	}
	var c []container
	if err := json.NewDecoder(bytes.NewReader(out)).Decode(&c); err != nil {
		return "", err
	}
	if len(c) == 0 {
		return "", errors.New("no output from docker inspect")
	}
	if ip := c[0].NetworkSettings.IPAddress; ip != "" {
		return ip, nil
	}
	return "", errors.New("no IP. Not running?")
}
