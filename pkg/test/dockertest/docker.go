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
Package dockertest contains helper functions for setting up and tearing down docker containers to aid in testing.
*/
package dockertest

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"testing"
	"time"

	"camlistore.org/pkg/netutil"
)

/// runLongTest checks all the conditions for running a docker container
// based on image.
func runLongTest(t *testing.T, image string) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	if !haveDocker() {
		t.Skip("skipping test; 'docker' command not found")
	}
	if ok, err := haveImage(image); !ok || err != nil {
		if err != nil {
			t.Skipf("Error running docker to check for %s: %v", image, err)
		}
		log.Printf("Pulling docker image %s ...", image)
		if err := Pull(image); err != nil {
			t.Skipf("Error pulling %s: %v", image, err)
		}
	}
}

// haveDocker returns whether the "docker" command was found.
func haveDocker() bool {
	_, err := exec.LookPath("docker")
	return err == nil
}

func haveImage(name string) (ok bool, err error) {
	out, err := exec.Command("docker", "images", "--no-trunc").Output()
	if err != nil {
		return
	}
	return bytes.Contains(out, []byte(name)), nil
}

func run(args ...string) (containerID string, err error) {
	cmd := exec.Command("docker", append([]string{"run"}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err = cmd.Run(); err != nil {
		err = fmt.Errorf("%v%v", stderr.String(), err)
		return
	}
	containerID = strings.TrimSpace(stdout.String())
	if containerID == "" {
		return "", errors.New("unexpected empty output from `docker run`")
	}
	return
}

func KillContainer(container string) error {
	return exec.Command("docker", "kill", container).Run()
}

// Pull retrieves the docker image with 'docker pull'.
func Pull(image string) error {
	out, err := exec.Command("docker", "pull", image).CombinedOutput()
	if err != nil {
		err = fmt.Errorf("%v: %s", err, out)
	}
	return err
}

// IP returns the IP address of the container.
func IP(containerID string) (string, error) {
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
	return "", fmt.Errorf("could not find an IP for %v. Not running?", containerID)
}

type ContainerID string

func (c ContainerID) IP() (string, error) {
	return IP(string(c))
}

func (c ContainerID) Kill() error {
	return KillContainer(string(c))
}

// lookup retrieves the ip address of the container, and tries to reach
// before timeout the tcp address at this ip and given port.
func (c ContainerID) lookup(port int, timeout time.Duration) (ip string, err error) {
	ip, err = c.IP()
	if err != nil {
		err = fmt.Errorf("Error getting container IP: %v", err)
		return
	}
	addr := fmt.Sprintf("%s:%d", ip, port)
	if err = netutil.AwaitReachable(addr, timeout); err != nil {
		err = fmt.Errorf("timeout trying to reach %s for container %v: %v", addr, c, err)
	}
	return
}

// setupContainer sets up a container, using the start function to run the given image.
// It also looks up the IP address of the container, and tests this address with the given
// port and timeout. It returns the container ID and its IP address, or makes the test
// fail on error.
func setupContainer(t *testing.T, image string, port int, timeout time.Duration,
	start func() (string, error)) (c ContainerID, ip string) {
	runLongTest(t, image)

	containerID, err := start()
	if err != nil {
		t.Fatalf("docker run: %v", err)
	}
	c = ContainerID(containerID)
	ip, err = c.lookup(port, timeout)
	if err != nil {
		c.Kill()
		t.Fatalf("container lookup: %v", err)
	}
	return
}

const (
	mongoImage       = "robinvdvleuten/mongo"
	mysqlImage       = "orchardup/mysql"
	MySQLUsername    = "root"
	MySQLPassword    = "root"
	postgresImage    = "nornagon/postgres"
	PostgresUsername = "docker" // set up by the dockerfile of postgresImage
	PostgresPassword = "docker" // set up by the dockerfile of postgresImage
)

// SetupMongoContainer sets up a real MongoDB instance for testing purposes,
// using a Docker container. It returns the container ID and its IP address,
// or makes the test fail on error.
// Currently using https://index.docker.io/u/robinvdvleuten/mongo/
func SetupMongoContainer(t *testing.T) (c ContainerID, ip string) {
	return setupContainer(t, mongoImage, 27017, 20*time.Second, func() (string, error) {
		return run("-d", mongoImage, "--smallfiles")
	})
}

// SetupMySQLContainer sets up a real MySQL instance for testing purposes,
// using a Docker container. It returns the container ID and its IP address,
// or makes the test fail on error.
// Currently using https://index.docker.io/u/orchardup/mysql/
func SetupMySQLContainer(t *testing.T, dbname string) (c ContainerID, ip string) {
	return setupContainer(t, mysqlImage, 3306, 10*time.Second, func() (string, error) {
		return run("-d", "-e", "MYSQL_ROOT_PASSWORD="+MySQLPassword, "-e", "MYSQL_DATABASE="+dbname, mysqlImage)
	})
}

// SetupPostgreSQLContainer sets up a real PostgreSQL instance for testing purposes,
// using a Docker container. It returns the container ID and its IP address,
// or makes the test fail on error.
// Currently using https://index.docker.io/u/nornagon/postgres
func SetupPostgreSQLContainer(t *testing.T, dbname string) (c ContainerID, ip string) {
	c, ip = setupContainer(t, postgresImage, 5432, 10*time.Second, func() (string, error) {
		return run("-d", postgresImage)
	})
	cleanupAndDie := func(err error) {
		c.Kill()
		t.Fatal(err)
	}
	// Otherwise getting error: "pq: the database system is starting up"
	// TODO(mpl): solution that adapts to the machine's perfs?
	time.Sleep(2 * time.Second)
	rootdb, err := sql.Open("postgres",
		fmt.Sprintf("user=%s password=%s host=%s dbname=postgres sslmode=disable", PostgresUsername, PostgresPassword, ip))
	if err != nil {
		cleanupAndDie(fmt.Errorf("Could not open postgres rootdb: %v", err))
	}
	if _, err := rootdb.Exec("CREATE DATABASE " + dbname + " LC_COLLATE = 'C' TEMPLATE = template0"); err != nil {
		cleanupAndDie(fmt.Errorf("Could not create database %v: %v", dbname, err))
	}
	return
}
