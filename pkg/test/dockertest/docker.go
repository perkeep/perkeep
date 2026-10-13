/*
Copyright 2014 The Perkeep Authors

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
package dockertest // import "perkeep.org/pkg/test/dockertest"

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"maps"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"perkeep.org/internal/netutil"
)

// Debug when set, prevents any container from being removed.
var Debug bool

// / runLongTest checks all the conditions for running a docker container
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
		return fmt.Errorf("error fetching image %s: %w", image, err)
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
			errc1 <- fmt.Errorf("gzip reader error for image %s: %w", image, err)
			return
		}
		defer zr.Close()
		if _, err = io.Copy(tar, zr); err != nil {
			errc1 <- fmt.Errorf("error gunzipping image %s: %w", image, err)
			return
		}
		errc1 <- nil
	}()
	go func() {
		if err := dockerLoad.Run(); err != nil {
			errc2 <- fmt.Errorf("error running docker load %v: %w", image, err)
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
}

// haveDocker returns whether the "docker" command was found.
func haveDocker() bool {
	_, err := exec.LookPath("docker")
	return err == nil
}

// haveImage reports whether we have the the given docker image. The name can
// either be of the <repository>, or <image id>, or <repository:tag> form.
func haveImage(name string) (ok bool, err error) {
	out, err := exec.Command("docker", "images", "--no-trunc").Output()
	if err != nil {
		return
	}
	fields := strings.Split(name, ":")
	if len(fields) < 2 {
		return bytes.Contains(out, []byte(name)), nil
	}
	tag := fields[1]
	image := fields[0]
	sc := bufio.NewScanner(bytes.NewBuffer(out))
	for sc.Scan() {
		l := sc.Text()
		if !strings.HasPrefix(l, image) {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(l, image)), tag) {
			return true, nil
		}
	}
	return false, sc.Err()
}

func run(args ...string) (containerID string, err error) {
	cmd := exec.Command("docker", append([]string{"run"}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err = cmd.Run(); err != nil {
		err = fmt.Errorf("%v%w", stderr.String(), err)
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
		return fmt.Errorf("docker pull failed: stdout: %s, stderr: %s, err: %w", out, stderr.String(), err)
	}
	return nil
}

// IP returns the IP address of the container.
func IP(containerID string) (string, error) {
	out, err := exec.Command("docker", "inspect", containerID).Output()
	if err != nil {
		return "", err
	}
	type network struct {
		IPAddress string
	}
	type networkSettings struct {
		IPAddress string             // not reported since Docker 29
		Networks  map[string]network // by network name
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
	ns := c[0].NetworkSettings
	if ns.IPAddress != "" {
		return ns.IPAddress, nil
	}
	for _, name := range slices.Sorted(maps.Keys(ns.Networks)) {
		if ip := ns.Networks[name].IPAddress; ip != "" {
			return ip, nil
		}
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
		err = fmt.Errorf("error getting IP: %w", err)
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
	mysqlImage       = "mysql:8"
	MySQLUsername    = "root"
	MySQLPassword    = "root"
	postgresImage    = "postgres:18"
	PostgresUsername = "postgres"
	PostgresPassword = "postgres"
	camliHub         = "https://storage.googleapis.com/camlistore-docker/"
)

// DB is a database that a test can use.
type DB struct {
	// Host is the value for the "host" key of a sorted.KeyValue config.
	Host string
	// Database is the name of the database, unique to the test.
	Database string
}

// newDBName returns a new, unique database name.
func newDBName() string {
	return "camlitest_" + strings.ToLower(rand.Text())
}

// SetupMySQL returns a MySQL database for the test, accessible with
// MySQLUsername and MySQLPassword. The database might not exist yet.
//
// If the PERKEEP_TEST_MYSQL_HOST environment variable is set, the database
// is on that already-running server (host[:port]) and is dropped when the
// test ends, and the test fails if the server is unreachable. Otherwise,
// SetupMySQL runs a server in a new Docker container
// (https://hub.docker.com/_/mysql/), or skips the test if it can't.
func SetupMySQL(t *testing.T) DB {
	db := DB{Database: newDBName()}
	if host := os.Getenv("PERKEEP_TEST_MYSQL_HOST"); host != "" {
		if !strings.Contains(host, ":") {
			host += ":3306"
		}
		db.Host = host
		rootdb, err := sql.Open("mysql", fmt.Sprintf("%s:%s@tcp(%s)/", MySQLUsername, MySQLPassword, host))
		if err != nil {
			t.Fatalf("opening MySQL at %s: %v", host, err)
		}
		if err := rootdb.Ping(); err != nil {
			rootdb.Close()
			t.Fatalf("MySQL server at %s (from PERKEEP_TEST_MYSQL_HOST) unreachable: %v", host, err)
		}
		t.Cleanup(func() {
			defer rootdb.Close()
			if _, err := rootdb.Exec("DROP DATABASE IF EXISTS " + db.Database); err != nil {
				t.Errorf("dropping MySQL database %s: %v", db.Database, err)
			}
		})
		return db
	}
	c, ip := setupContainer(t, mysqlImage, 3306, 20*time.Second, func() (string, error) {
		return run("-d", "-e", "MYSQL_ROOT_PASSWORD="+MySQLPassword, "-e", "MYSQL_DATABASE="+db.Database, mysqlImage)
	})
	t.Cleanup(func() { c.KillRemove(t) })
	db.Host = ip + ":3306"
	return db
}

// SetupPostgreSQL returns a new PostgreSQL database for the test, using the
// C collation, accessible with PostgresUsername and PostgresPassword.
//
// If the PERKEEP_TEST_POSTGRES_HOST environment variable is set, the database
// is on that already-running server (on the default port) and is dropped when
// the test ends, and the test fails if the server is unreachable. Otherwise,
// SetupPostgreSQL runs a server in a new Docker container
// (https://hub.docker.com/_/postgres/), or skips the test if it can't.
func SetupPostgreSQL(t *testing.T) DB {
	db := DB{Database: newDBName()}
	host := os.Getenv("PERKEEP_TEST_POSTGRES_HOST")
	external := host != ""
	if external {
		db.Host = host
	} else {
		c, ip := setupContainer(t, postgresImage, 5432, 15*time.Second, func() (string, error) {
			return run("-d", "-e", "POSTGRES_USER="+PostgresUsername, "-e", "POSTGRES_PASSWORD="+PostgresPassword, postgresImage)
		})
		t.Cleanup(func() { c.KillRemove(t) })
		db.Host = ip
	}
	rootdb, err := sql.Open("postgres",
		fmt.Sprintf("user=%s password=%s host=%s dbname=postgres sslmode=disable", PostgresUsername, PostgresPassword, db.Host))
	if err != nil {
		t.Fatalf("Could not open postgres rootdb: %v", err)
	}
	t.Cleanup(func() { rootdb.Close() })
	create := "CREATE DATABASE " + db.Database + " LC_COLLATE = 'C' TEMPLATE = template0"
	if !external {
		// The new container might not accept connections yet.
		if _, err := sqlExecRetry(rootdb, create, 50); err != nil {
			t.Fatalf("Could not create database %v: %v", db.Database, err)
		}
		return db
	}
	if _, err := rootdb.Exec(create); err != nil {
		t.Fatalf("creating database on PostgreSQL server at %s (from PERKEEP_TEST_POSTGRES_HOST): %v", db.Host, err)
	}
	t.Cleanup(func() {
		if _, err := rootdb.Exec("DROP DATABASE IF EXISTS " + db.Database + " WITH (FORCE)"); err != nil {
			t.Errorf("dropping PostgreSQL database %s: %v", db.Database, err)
		}
	})
	return db
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
	return result, fmt.Errorf("failed %v times: %w", try, err)
}
