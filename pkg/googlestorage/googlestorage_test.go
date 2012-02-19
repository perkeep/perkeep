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

// FYI These tests are integration tests that need to run against google
// storage. See the README for more details on necessary setup

package googlestorage

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/third_party/code.google.com/goauth2/oauth"
)

const testObjectContent = "Google Storage Test\n"

type BufferCloser struct {
	*bytes.Buffer
}

func (b *BufferCloser) Close() error {
	b.Reset()
	return nil
}

// Reads google storage config and creates a Client.  Exits on error.
func doConfig(t *testing.T) (gsa *Client, bucket string) {
	cf, err := jsonconfig.ReadFile("testconfig.json")
	if err != nil {
		t.Fatalf("Failed to read config: %v", err)
	}

	var config jsonconfig.Obj
	config = cf.RequiredObject("gsconf")
	if err := cf.Validate(); err != nil {
		t.Fatalf("Invalid config: %v", err)
	}

	auth := config.RequiredObject("auth")
	bucket = config.RequiredString("bucket")
	if err := config.Validate(); err != nil {
		t.Fatalf("Invalid config: %v", err)
	}

	gsa = NewClient(&oauth.Transport{
		&oauth.Config{
			ClientId:     auth.RequiredString("client_id"),
			ClientSecret: auth.RequiredString("client_secret"),
			Scope:        "https://www.googleapis.com/auth/devstorage.read_write",
			AuthURL:      "https://accounts.google.com/o/oauth2/auth",
			TokenURL:     "https://accounts.google.com/o/oauth2/token",
			RedirectURL:  "urn:ietf:wg:oauth:2.0:oob",
		},
		&oauth.Token{
			AccessToken:  "",
			RefreshToken: auth.RequiredString("refresh_token"),
			TokenExpiry:  0,
		},
		nil,
	})

	if err := auth.Validate(); err != nil {
		t.Fatalf("Invalid config: %v", err)
	}
	return
}

func TestGetObject(t *testing.T) {
	gs, bucket := doConfig(t)

	body, size, err := gs.GetObject(&Object{bucket, "test-get"})
	if err != nil {
		t.Fatalf("Fetch failed: %v\n", err)
	}

	content := make([]byte, size)
	if _, err = body.Read(content); err != nil {
		t.Fatalf("Failed to read response body: %v:\n", err)
	}

	if string(content) != testObjectContent {
		t.Fatalf("Object has incorrect content.\nExpected: '%v'\nFound: '%v'\n", testObjectContent, string(content))
	}
}

func TestStatObject(t *testing.T) {
	gs, bucket := doConfig(t)

	// Stat a nonexistant file
	size, exists, err := gs.StatObject(&Object{bucket, "test-shouldntexist"})
	if err != nil {
		t.Errorf("Stat failed: %v\n", err)
	}
	if exists {
		t.Errorf("Test object exists!")
	}
	if size != 0 {
		t.Errorf("Expected size to be 0, found %v\n", size)
	}

	// Try statting an object which does exist
	size, exists, err = gs.StatObject(&Object{bucket, "test-stat"})
	if err != nil {
		t.Errorf("Stat failed: %v\n", err)
	}
	if !exists {
		t.Errorf("Test object doesn't exist!")
	}
	if size != int64(len(testObjectContent)) {
		t.Errorf("Test object size is wrong: \nexpected: %v\nfound: %v\n",
			len(testObjectContent), size)
	}
}

func TestPutObject(t *testing.T) {
	gs, bucket := doConfig(t)

	now := time.Now().UTC()
	testKey := fmt.Sprintf("test-put-%v.%v.%v-%v.%v.%v",
		now.Year, now.Month, now.Day, now.Hour, now.Minute, now.Second)

	shouldRetry, err := gs.PutObject(&Object{bucket, testKey},
		&BufferCloser{bytes.NewBufferString(testObjectContent)})
	if shouldRetry {
		shouldRetry, err = gs.PutObject(&Object{bucket, testKey},
			&BufferCloser{bytes.NewBufferString(testObjectContent)})
	}
	if err != nil {
		t.Fatalf("Failed to put object: %v", err)
	}

	// Just stat to check that it actually uploaded, don't bother reading back
	size, exists, err := gs.StatObject(&Object{bucket, testKey})
	if !exists {
		t.Errorf("Test object doesn't exist!")
	}
	if size != int64(len(testObjectContent)) {
		t.Errorf("Test object size is wrong: \nexpected: %v\nfound: %v\n",
			len(testObjectContent), size)
	}
}

func TestDeleteObject(t *testing.T) {
	gs, bucket := doConfig(t)

	// Try deleting a nonexitent file
	err := gs.DeleteObject(&Object{bucket, "test-shouldntexist"})
	if err == nil {
		t.Errorf("Tried to delete nonexistent object, succeeded.")
	}

	// Create a file, try to delete it
	now := time.Now().UTC()
	testKey := fmt.Sprintf("test-delete-%v.%v.%v-%v.%v.%v",
		now.Year, now.Month, now.Day, now.Hour, now.Minute, now.Second)
	_, err = gs.PutObject(&Object{bucket, testKey},
		&BufferCloser{bytes.NewBufferString("Delete Me")})
	if err != nil {
		t.Fatalf("Failed to put file to delete.")
	}
	err = gs.DeleteObject(&Object{bucket, testKey})
	if err != nil {
		t.Errorf("Failed to delete object: %v", err)
	}
}

func TestEnumerateBucket(t *testing.T) {
	gs, bucket := doConfig(t)

	// Enumerate ALL the things!
	objs, err := gs.EnumerateObjects(bucket, "", 0)
	if err != nil {
		t.Errorf("Enumeration failed: %v\n", err)
	} else if len(objs) < 7 {
		// Minimum number of blobs, equal to the number of files in testdata
		t.Errorf("Expected at least 7 files, found %v", len(objs))
	}

	// Test a limited enum
	objs, err = gs.EnumerateObjects(bucket, "", 5)
	if err != nil {
		t.Errorf("Enumeration failed: %v\n", err)
	} else if len(objs) != 5 {
		t.Errorf(
			"Limited enum returned wrong number of blobs.\nExpected: %v\nFound: %v",
			5, len(objs))
	}

	// Test fetching a limited set from a known start point
	objs, err = gs.EnumerateObjects(bucket, "test-enum", 4)
	if err != nil {
		t.Errorf("Enumeration failed: %v\n", err)
	} else {
		for i := 0; i < 4; i += 1 {
			if objs[i].Key != fmt.Sprintf("test-enum-%v", i+1) {
				t.Errorf(
					"Enum from start point returned wrong key:\nExpected: test-enum-%v\nFound: %v",
					i+1, objs[i].Key)
			}
		}
	}
}
