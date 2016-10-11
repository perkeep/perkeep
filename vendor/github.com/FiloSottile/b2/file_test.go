package b2_test

import (
	"bytes"
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"reflect"
	"testing"
	"time"

	"github.com/FiloSottile/b2"
)

func getBucket(t *testing.T, c *b2.Client) *b2.BucketInfo {
	r := make([]byte, 6)
	rand.Read(r)
	name := "test-" + hex.EncodeToString(r)

	b, err := c.CreateBucket(name, false)
	if err != nil {
		t.Fatal(err)
	}

	return b
}

func TestFileLifecycle(t *testing.T) {
	c := getClient(t)
	b := getBucket(t, c)
	defer b.Delete()

	file := make([]byte, 123456)
	rand.Read(file)
	fiu, err := b.Upload(bytes.NewReader(file), "test-foo", "")
	if err != nil {
		t.Fatal(err)
	}

	fi, err := c.GetFileInfoByID(fiu.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fi.ID != fiu.ID {
		t.Error("Mismatched file ID")
	}
	if fi.ContentLength != 123456 {
		t.Error("Mismatched file length")
	}
	if fi.Name != "test-foo" {
		t.Error("Mismatched file name")
	}
	if fi.UploadTimestamp.After(time.Now()) || fi.UploadTimestamp.Before(time.Now().Add(-time.Hour)) {
		t.Error("Wrong UploadTimestamp")
	}
	if fi.ContentSHA1 != fiu.ContentSHA1 {
		t.Error("Mismatched SHA1")
	}
	digest := sha1.Sum(file)
	if fi.ContentSHA1 != hex.EncodeToString(digest[:]) {
		t.Error("Wrong SHA1")
	}

	fi, err = b.GetFileInfoByName("test-foo")
	if err != nil {
		t.Fatal(err)
	}
	if fi.ID != fiu.ID {
		t.Error("Mismatched file ID in GetByName")
	}
	_, err = b.GetFileInfoByName("not-exists")
	if err != b2.FileNotFoundError {
		t.Errorf("b.GetFileInfoByName did not return FileNotFoundError: %v", err)
	}

	rc, fi2, err := c.DownloadFileByID(fiu.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fi2.UploadTimestamp != fi.UploadTimestamp {
		t.Error("mismatch in c.DownloadFileByID -> fi.UploadTimestamp")
	}
	if fi2.ContentSHA1 != fi.ContentSHA1 {
		t.Error("mismatch in c.DownloadFileByID -> fi.ContentSHA1")
	}
	if fi2.ContentLength != 123456 {
		t.Error("mismatch in c.DownloadFileByID -> fi.ContentLength")
	}
	body, err := ioutil.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, file) {
		t.Error("mismatch in file contents")
	}
	rc.Close()

	rc, fi3, err := c.DownloadFileByName(b.Name, "test-foo")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fi2, fi3) {
		t.Error("DownloadFileByID.FileInfo != DownloadFileByName.FileInfo")
	}
	body, err = ioutil.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, file) {
		t.Error("mismatch in file contents")
	}
	rc.Close()

	if err := c.DeleteFile(fiu.ID, "test-foo"); err != nil {
		t.Fatal(err)
	}
}

func TestFileListing(t *testing.T) {
	c := getClient(t)
	b := getBucket(t, c)
	defer b.Delete()

	file := make([]byte, 1234)
	rand.Read(file)

	for i := 0; i < 2; i++ {
		if _, err := b.Upload(bytes.NewReader(file), "test-3", ""); err != nil {
			t.Fatal(err)
		}
	}

	var fileIDs []string
	for i := 0; i < 5; i++ {
		fi, err := b.Upload(bytes.NewReader(file), fmt.Sprintf("test-%d", i), "")
		if err != nil {
			t.Fatal(err)
		}
		fileIDs = append(fileIDs, fi.ID)
	}

	i, l := 0, b.ListFiles("")
	for l.Next() {
		fi := l.FileInfo()
		if fi.ID != fileIDs[i] {
			t.Errorf("wrong file ID number %d: expected %s, got %s", i, fileIDs[i], fi.ID)
		}
		i++
	}
	if err := l.Err(); err != nil {
		t.Fatal(err)
	}
	if i != len(fileIDs) {
		t.Errorf("got %d files, expected %d", i-1, len(fileIDs)-1)
	}

	i, l = 1, b.ListFiles("test-1")
	l.SetPageCount(3)
	for l.Next() {
		fi := l.FileInfo()
		if fi.ID != fileIDs[i] {
			t.Errorf("wrong file ID number %d: expected %s, got %s", i, fileIDs[i], fi.ID)
		}
		i++
	}
	if err := l.Err(); err != nil {
		t.Fatal(err)
	}
	if i != len(fileIDs) {
		t.Errorf("got %d files, expected %d", i-1, len(fileIDs)-1)
	}

	i, l = 0, b.ListFilesVersions("", "")
	l.SetPageCount(2)
	for l.Next() {
		i++
	}
	if err := l.Err(); err != nil {
		t.Fatal(err)
	}
	if i != len(fileIDs)+2 {
		t.Errorf("got %d files, expected %d", i-1, len(fileIDs)-1+2)
	}
}
