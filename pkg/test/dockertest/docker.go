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
package dockertest // import "camlistore.org/pkg/test/dockertest"

import (
	"bytes"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"camlistore.org/pkg/netutil"
)

// Debug, if set, prevents any container from being removed.
var Debug bool

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
		if strings.HasPrefix(image, "camlistore/") {
			if err := loadCamliHubImage(image); err != nil {
				t.Skipf("Error pulling %s: %v", image, err)
			}
			return
		}
		if err := Pull(image); err != nil {
			t.Skipf("Error pulling %s: %v", image, err)
		}
	}
}

// loadCamliHubImage fetches a docker image saved as a .tar.gz in the
// camlistore-docker bucket, and loads it in docker.
func loadCamliHubImage(image string) error {
	if !strings.HasPrefix(image, "camlistore/") {
		return fmt.Errorf("not an image hosted on camlistore-docker")
	}
	imgURL := camliHub + strings.TrimPrefix(image, "camlistore/") + ".tar.gz"
	resp, err := http.Get(imgURL)
	if err != nil {
		return fmt.Errorf("error fetching image %s: %v", image, err)
	}
	defer resp.Body.Close()

	dockerLoad := exec.Command("docker", "load")
	dockerLoad.Stderr = os.Stderr
	tar, err := dockerLoad.StdinPipe()
	if err != nil {
		return err
	}
	errc1 := make(chan error)
	errc2 := make(chan error)
	go func() {
		defer tar.Close()
		zr, err := gzip.NewReader(resp.Body)
		if err != nil {
			errc1 <- fmt.Errorf("gzip reader error for image %s: %v", image, err)
			return
		}
		defer zr.Close()
		if _, err = io.Copy(tar, zr); err != nil {
			errc1 <- fmt.Errorf("error gunzipping image %s: %v", image, err)
			return
		}
		errc1 <- nil
	}()
	go func() {
		if err := dockerLoad.Run(); err != nil {
			errc2 <- fmt.Errorf("error running docker load %v: %v", image, err)
			return
		}
		errc2 <- nil
	}()
	select {
	case err := <-errc1:
		if err != nil {
			return err
		}
		return <-errc2
	case err := <-errc2:
		if err != nil {
			return err
		}
		return <-errc1
	}
	return nil
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
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("docker", "pull", image)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	out := stdout.String()
	// TODO(mpl): if it turns out docker respects conventions and the
	// "Authentication is required" message does come from stderr, then quit
	// checking stdout.
	if err != nil || stderr.Len() != 0 || strings.Contains(out, "Authentication is required") {
		return fmt.Errorf("docker pull failed: stdout: %s, stderr: %s, err: %v", out, stderr.String(), err)
	}
	return nil
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
	return "", errors.New("could not find an IP. Not running?")
}

type ContainerID string

func (c ContainerID) IP() (string, error) {
	return IP(string(c))
}

func (c ContainerID) Kill() error {
	if string(c) == "" {
		return nil
	}
	return KillContainer(string(c))
}

// Remove runs "docker rm" on the container
func (c ContainerID) Remove() error {
	if Debug {
		return nil
	}
	if string(c) == "" {
		return nil
	}
	return exec.Command("docker", "rm", "-v", string(c)).Run()
}

// KillRemove calls Kill on the container, and then Remove if there was
// no error. It logs any error to t.
func (c ContainerID) KillRemove(t *testing.T) {
	if err := c.Kill(); err != nil {
		t.Log(err)
		return
	}
	if err := c.Remove(); err != nil {
		t.Log(err)
	}
}

// lookup retrieves the ip address of the container, and tries to reach
// before timeout the tcp address at this ip and given port.
func (c ContainerID) lookup(port int, timeout time.Duration) (ip string, err error) {
	ip, err = c.IP()
	if err != nil {
		err = fmt.Errorf("error getting IP: %v", err)
		return
	}
	addr := fmt.Sprintf("%s:%d", ip, port)
	err = netutil.AwaitReachable(addr, timeout)
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
		c.KillRemove(t)
		t.Skipf("Skipping test for container %v: %v", c, err)
	}
	return
}

const (
	mongoImage       = "mpl7/mongo"
	mysqlImage       = "mysql"
	MySQLUsername    = "root"
	MySQLPassword    = "root"
	postgresImage    = "nornagon/postgres"
	PostgresUsername = "docker" // set up by the dockerfile of postgresImage
	PostgresPassword = "docker" // set up by the dockerfile of postgresImage
	camliHub         = "https://storage.googleapis.com/camlistore-docker/"
	fakeS3Image      = "camlistore/fakes3"
)

func SetupFakeS3Container(t *testing.T) (c ContainerID, ip string) {
	return setupContainer(t, fakeS3Image, 4567, 10*time.Second, func() (string, error) {
		return run("-d", fakeS3Image)
	})
}

// SetupMongoContainer sets up a real MongoDB instance for testing purposes,
// using a Docker container. It returns the container ID and its IP address,
// or makes the test fail on error.
// Currently using https://index.docker.io/u/robinvdvleuten/mongo/
func SetupMongoContainer(t *testing.T) (c ContainerID, ip string) {
	return setupContainer(t, mongoImage, 27017, 10*time.Second, func() (string, error) {
		return run("-d", mongoImage, "--nojournal")
	})
}

// SetupMySQLContainer sets up a real MySQL instance for testing purposes,
// using a Docker container. It returns the container ID and its IP address,
// or makes the test fail on error.
// Currently using https://hub.docker.com/_/mysql/
func SetupMySQLContainer(t *testing.T, dbname string) (c ContainerID, ip string) {
	return setupContainer(t, mysqlImage, 3306, 20*time.Second, func() (string, error) {
		return run("-d", "-e", "MYSQL_ROOT_PASSWORD="+MySQLPassword, "-e", "MYSQL_DATABASE="+dbname, mysqlImage)
	})
}

// SetupPostgreSQLContainer sets up a real PostgreSQL instance for testing purposes,
// using a Docker container. It returns the container ID and its IP address,
// or makes the test fail on error.
// Currently using https://index.docker.io/u/nornagon/postgres
func SetupPostgreSQLContainer(t *testing.T, dbname string) (c ContainerID, ip string) {
	c, ip = setupContainer(t, postgresImage, 5432, 15*time.Second, func() (string, error) {
		return run("-d", postgresImage)
	})
	cleanupAndDie := func(err error) {
		c.KillRemove(t)
		t.Fatal(err)
	}
	rootdb, err := sql.Open("postgres",
		fmt.Sprintf("user=%s password=%s host=%s dbname=postgres sslmode=disable", PostgresUsername, PostgresPassword, ip))
	if err != nil {
		cleanupAndDie(fmt.Errorf("Could not open postgres rootdb: %v", err))
	}
	if _, err := sqlExecRetry(rootdb,
		"CREATE DATABASE "+dbname+" LC_COLLATE = 'C' TEMPLATE = template0",
		50); err != nil {
		cleanupAndDie(fmt.Errorf("Could not create database %v: %v", dbname, err))
	}
	return
}

// sqlExecRetry keeps calling http://golang.org/pkg/database/sql/#DB.Exec on db
// with stmt until it succeeds or until it has been tried maxTry times.
// It sleeps in between tries, twice longer after each new try, starting with
// 100 milliseconds.
func sqlExecRetry(db *sql.DB, stmt string, maxTry int) (sql.Result, error) {
	if maxTry <= 0 {
		return nil, errors.New("did not try at all")
	}
	interval := 100 * time.Millisecond
	try := 0
	var err error
	var result sql.Result
	for {
		result, err = db.Exec(stmt)
		if err == nil {
			return result, nil
		}
		try++
		if try == maxTry {
			break
		}
		time.Sleep(interval)
		interval *= 2
	}
	return result, fmt.Errorf("failed %v times: %v", try, err)
}
