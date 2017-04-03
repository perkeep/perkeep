package b2_test

import (
	"bytes"
	"crypto/rand"
	"io"
	"io/ioutil"
	"os"
	"testing"
)

func TestUploadError(t *testing.T) {
	c := getClient(t)
	b := getBucket(t, c)
	defer deleteBucket(t, b)

	file := make([]byte, 123456)
	rand.Read(file)
	_, err := b.Upload(bytes.NewReader(file), "illegal//filename", "")
	if err == nil {
		t.Fatal("Expected an error")
	}
	t.Log(err)
}

func TestUploadFile(t *testing.T) {
	c := getClient(t)
	b := getBucket(t, c)
	defer deleteBucket(t, b)

	tmpfile, err := ioutil.TempFile("", "b2")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	content := make([]byte, 123456)
	rand.Read(content)
	if _, err := tmpfile.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(tmpfile.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	fi, err := b.Upload(f, "foo-file", "")
	if err != nil {
		t.Fatal(err)
	}
	defer c.DeleteFile(fi.ID, fi.Name)
	if fi.ContentLength != 123456 {
		t.Error("mismatched fi.ContentLength", fi.ContentLength)
	}
	if n, err := io.Copy(ioutil.Discard, f); err != nil || n != 0 {
		t.Error("should have read 0 bytes:", n, err)
	}
}

func TestUploadBuffer(t *testing.T) {
	c := getClient(t)
	b := getBucket(t, c)
	defer deleteBucket(t, b)

	content := make([]byte, 123456)
	rand.Read(content)
	buf := bytes.NewBuffer(content)
	fi, err := b.Upload(buf, "foo-file", "")
	if err != nil {
		t.Fatal(err)
	}
	defer c.DeleteFile(fi.ID, fi.Name)
	if fi.ContentLength != 123456 {
		t.Error("mismatched fi.ContentLength", fi.ContentLength)
	}
	if buf.Len() != 0 {
		t.Error("Buffer is not empty")
	}
}

func TestUploadReader(t *testing.T) {
	c := getClient(t)
	b := getBucket(t, c)
	defer deleteBucket(t, b)

	content := make([]byte, 123456)
	rand.Read(content)
	r := bytes.NewReader(content)
	fi, err := b.Upload(ioutil.NopCloser(r), "foo-file", "") // shadow Seek method
	if err != nil {
		t.Fatal(err)
	}
	defer c.DeleteFile(fi.ID, fi.Name)
	if fi.ContentLength != 123456 {
		t.Error("mismatched fi.ContentLength", fi.ContentLength)
	}
	if r.Len() != 0 {
		t.Error("Reader is not empty")
	}
}
