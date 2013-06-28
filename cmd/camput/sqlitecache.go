/*
Copyright 2013 The Camlistore Authors.

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

package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/client"
	"camlistore.org/pkg/osutil"
)

const (
	cmdName         = "sqlite3"
	noResult        = "no row"
	haveTableName   = "have"
	statTableName   = "stat"
	testTable       = `.tables`
	createHaveTable = `CREATE TABLE ` + haveTableName +
		` (blobref VARCHAR(255) NOT NULL PRIMARY KEY,size INT)`
	createStatTable = `CREATE TABLE ` + statTableName +
		` (key TEXT NOT NULL PRIMARY KEY, val TEXT)`
	// Because of blocking reads on the output, we want to print
	// something even when a query returns no result,
	// hence the ugly joins.
	// TODO(mpl): there's probably a way to do non blocking reads
	// on the stdout pipe of the sqlite process, so we would not
	// have to use these ugly requests. Suggestion?
	blobSizeQuery = `SELECT COALESCE(size, fake.filler) as size
		FROM (SELECT '` + noResult + `' AS [filler]) fake
		LEFT JOIN ` + haveTableName +
		` ON blobref = `
	statKeyQuery = `SELECT COALESCE(val, fake.filler) as val
		FROM (SELECT '` + noResult + `' AS [filler]) fake
		LEFT JOIN ` + statTableName +
		` ON key = `
	noteHaveStmt = `INSERT INTO ` + haveTableName +
		` VALUES ('?1', ?2)` + ";\n"
	noteStatStmt = `INSERT INTO ` + statTableName +
		` VALUES ('?1', '?2')` + ";\n"
	keyNotUnique  = "column key is not unique\n"
	brefNotUnique = "column blobref is not unique\n"
)

func checkCmdInstalled() {
	_, err := exec.LookPath(cmdName)
	if err != nil {
		hint := `The binary is not in your $PATH or most likely not installed.` +
			` On debian based distributions, it is usually provided by the sqlite3 package.`
		log.Fatalf("%v command could not be found: %v\n"+hint, cmdName, err)
	}
}

type childInfo struct {
	r    *bufio.Reader  // to read the child's stdout
	w    io.WriteCloser // to write to the child's stdin
	proc *os.Process
	er   *bufio.Reader // to read the child's stderr
}

func startChild(filename string) (*childInfo, error) {
	cmdPath, err := exec.LookPath(cmdName)
	if err != nil {
		return nil, err
	}
	pr1, pw1, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	pr2, pw2, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	pr3, pw3, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	args := []string{cmdPath, filename}
	fds := []*os.File{pr1, pw2, pw3}
	p, err := os.StartProcess(cmdPath, args, &os.ProcAttr{Dir: "/", Files: fds})
	if err != nil {
		return nil, err
	}
	return &childInfo{
		r:    bufio.NewReader(pr2),
		w:    pw1,
		proc: p,
		er:   bufio.NewReader(pr3),
	}, nil
}

// SQLiteStatCache is an UploadCache based on sqlite.
// sqlite3 is called as a child process so we can still
// cross-compile static ARM binaries for Android, and
// use the android system sqlite, rather than having to
// include a big copy of the sqlite libs.
// It stores rows with (key, value) pairs, where
// key = filepath|statFingerprint and
// value = PutResult.BlobRef.String()|PutResult.Size
type SQLiteStatCache struct {
	filename string
	proc     *os.Process
	mu       sync.Mutex     // Guards reads and writes to sqlite.
	r        *bufio.Reader  // where to read the output from the sqlite process
	w        io.WriteCloser // where to write queries/statements to the sqlite process
}

func NewSQLiteStatCache(gen string) *SQLiteStatCache {
	checkCmdInstalled()
	filename := filepath.Join(osutil.CacheDir(), "camput.statcache."+escapeGen(gen)+".db")
	out, err := exec.Command(cmdName, filename, testTable).Output()
	if err != nil {
		log.Fatalf("Failed to test for %v table existence: %v", statTableName, err)
	}
	if len(out) == 0 {
		// file or table does not exist
		err = exec.Command(cmdName, filename, createStatTable).Run()
		if err != nil {
			log.Fatalf("Failed to create %v table for stat cache: %v", statTableName, err)
		}
	} else {
		if string(out) != statTableName+"\n" {
			log.Fatalf("Wrong table name for stat cache; was expecting %v, got %q",
				haveTableName, out)
		}
	}
	return &SQLiteStatCache{
		filename: filename,
	}
}

func (c *SQLiteStatCache) startSQLiteChild() error {
	if c.proc != nil {
		return nil
	}
	ci, err := startChild(c.filename)
	if err != nil {
		return err
	}
	go func() {
		for {
			errStr, err := ci.er.ReadString('\n')
			if err != nil {
				log.Fatal(err)
			}
			if !strings.HasSuffix(errStr, keyNotUnique) {
				log.Fatalf("Error on stat cache: %v", errStr)
			}
		}
	}()
	c.r = ci.r
	c.w = ci.w
	c.proc = ci.proc
	return nil
}

// sqliteCacheKey returns the key used for a stat entry in the sqlite cache.
// It is the cleaned absolute path of joining pwd and filename,
// concatenated with a fingerprint based on the file's info. If
// -filenodes is being used, the suffix "|Perm" is also appended.
func sqliteCacheKey(pwd, filename string, fi os.FileInfo, withPermanode bool) string {
	var fullPath string
	if filepath.IsAbs(filename) {
		fullPath = filepath.Clean(filename)
	} else {
		fullPath = filepath.Join(pwd, filename)
	}
	key := fmt.Sprintf("%v|%v", fullPath, string(fileInfoToFingerprint(fi)))
	if withPermanode {
		return fmt.Sprintf("%v|Perm", key)
	}
	return key
}

func (c *SQLiteStatCache) CachedPutResult(pwd, filename string, fi os.FileInfo, withPermanode bool) (*client.PutResult, error) {
	key := sqliteCacheKey(pwd, filename, fi, withPermanode)
	query := fmt.Sprintf("%v'%v';\n", statKeyQuery, key)
	c.mu.Lock()
	err := c.startSQLiteChild()
	if err != nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("Could not start sqlite child process: %v", err)
	}
	_, err = c.w.Write([]byte(query))
	if err != nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("failed to query stat cache: %v", err)
	}
	out, err := c.r.ReadString('\n')
	if err != nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("failed to read stat cache query result: %v", err)
	}
	out = strings.TrimRight(out, "\n")
	c.mu.Unlock()

	if out == noResult {
		return nil, errCacheMiss
	}
	fields := strings.Split(out, "|")
	if len(fields) > 2 {
		return nil, fmt.Errorf("Invalid stat cache value; was expecting \"bref|size\", got %q", out)
	}
	br := blobref.Parse(fields[0])
	if br == nil {
		return nil, fmt.Errorf("Invalid blobref in stat cache: %q", fields[0])
	}
	blobSize, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("Invalid blob size %q in stat cache: %v", fields[1], err)
	}
	return &client.PutResult{
		BlobRef: br,
		Size:    blobSize,
		Skipped: true,
	}, nil
}

func (c *SQLiteStatCache) AddCachedPutResult(pwd, filename string, fi os.FileInfo, pr *client.PutResult, withPermanode bool) {
	key := sqliteCacheKey(pwd, filename, fi, withPermanode)
	val := pr.BlobRef.String() + "|" + strconv.FormatInt(pr.Size, 10)
	repl := strings.NewReplacer("?1", key, "?2", val)
	query := repl.Replace(noteStatStmt)
	c.mu.Lock()
	defer c.mu.Unlock()
	err := c.startSQLiteChild()
	if err != nil {
		log.Fatalf("Could not start sqlite child process: %v", err)
	}
	cachelog.Printf("Adding to stat cache %v: %v", key, val)
	_, err = c.w.Write([]byte(query))
	if err != nil {
		log.Fatalf("failed to write to stat cache: %v", err)
	}
}

// SQLiteHacheCache is a HaveCache based on sqlite.
// sqlite3 is called as a child process so we can still
// cross-compile static ARM binaries for Android, and
// use the android system sqlite, rather than having to
// include a big copy of the sqlite libs.
// It stores rows with (key,value) pairs, where
// key = blobref and
// value = blobsize
type SQLiteHaveCache struct {
	filename string
	proc     *os.Process
	mu       sync.Mutex     // Guards reads and writes to sqlite.
	r        *bufio.Reader  // where to read the output from the sqlite process
	w        io.WriteCloser // where to write queries/statements to the sqlite process
}

func NewSQLiteHaveCache(gen string) *SQLiteHaveCache {
	checkCmdInstalled()
	filename := filepath.Join(osutil.CacheDir(), "camput.havecache."+escapeGen(gen)+".db")
	out, err := exec.Command(cmdName, filename, testTable).Output()
	if err != nil {
		log.Fatalf("Failed to test for %v table existence: %v", haveTableName, err)
	}
	if len(out) == 0 {
		// file or table does not exist
		err = exec.Command(cmdName, filename, createHaveTable).Run()
		if err != nil {
			log.Fatalf("Failed to create %v table for have cache: %v", haveTableName, err)
		}
	} else {
		if string(out) != haveTableName+"\n" {
			log.Fatalf("Wrong table name for have cache; was expecting %v, got %q",
				haveTableName, out)
		}
	}
	return &SQLiteHaveCache{
		filename: filename,
	}
}

func (c *SQLiteHaveCache) startSQLiteChild() error {
	if c.proc != nil {
		return nil
	}
	ci, err := startChild(c.filename)
	if err != nil {
		return err
	}
	go func() {
		for {
			errStr, err := ci.er.ReadString('\n')
			if err != nil {
				log.Fatal(err)
			}
			if !strings.HasSuffix(errStr, brefNotUnique) {
				log.Fatalf("Error on have cache: %v", errStr)
			}
		}
	}()
	c.r = ci.r
	c.w = ci.w
	c.proc = ci.proc
	return nil
}

func (c *SQLiteHaveCache) StatBlobCache(br *blobref.BlobRef) (size int64, ok bool) {
	if br == nil {
		return
	}
	// TODO(mpl): is it enough that we know it's a valid blobref to avoid any injection risk ?
	query := blobSizeQuery + fmt.Sprintf("'%v';\n", br.String())
	c.mu.Lock()
	defer c.mu.Unlock()
	err := c.startSQLiteChild()
	if err != nil {
		log.Fatalf("Could not start sqlite child process: %v", err)
	}
	_, err = c.w.Write([]byte(query))
	if err != nil {
		log.Fatalf("failed to query have cache: %v", err)
	}
	out, err := c.r.ReadString('\n')
	if err != nil {
		log.Fatalf("failed to read have cache query result: %v", err)
	}
	out = strings.TrimRight(out, "\n")
	if out == noResult {
		return
	}
	size, err = strconv.ParseInt(out, 10, 64)
	if err != nil {
		log.Fatalf("Bogus blob size in %v table: %v", haveTableName, err)
	}
	return size, true
}

func (c *SQLiteHaveCache) NoteBlobExists(br *blobref.BlobRef, size int64) {
	if size < 0 {
		log.Fatalf("Got a negative blob size to note in have cache")
	}
	if br == nil {
		return
	}
	repl := strings.NewReplacer("?1", br.String(), "?2", fmt.Sprint(size))
	query := repl.Replace(noteHaveStmt)
	c.mu.Lock()
	defer c.mu.Unlock()
	err := c.startSQLiteChild()
	if err != nil {
		log.Fatalf("Could not start sqlite child process: %v", err)
	}
	_, err = c.w.Write([]byte(query))
	if err != nil {
		log.Fatalf("failed to write to have cache: %v", err)
	}
}
