package b2_test

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"net/http"
	"os"
	"sync"
	"testing"

	"github.com/FiloSottile/b2"
)

var client *b2.Client
var clientMu sync.Mutex

func getClient(t *testing.T) *b2.Client {
	accountID := os.Getenv("ACCOUNT_ID")
	applicationKey := os.Getenv("APPLICATION_KEY")
	if accountID == "" || applicationKey == "" {
		t.Fatal("Missing ACCOUNT_ID or APPLICATION_KEY")
	}
	clientMu.Lock()
	defer clientMu.Unlock()
	if client != nil {
		return client
	}
	c, err := b2.NewClient(accountID, applicationKey, &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
	})
	if err != nil {
		t.Fatal("While authenticating:", err)
	}
	client = c
	return c
}

func TestBucketLifecycle(t *testing.T) {
	c := getClient(t)

	r := make([]byte, 6)
	rand.Read(r)
	name := "test-" + hex.EncodeToString(r)

	if _, err := c.BucketByName(name, false); err == nil {
		t.Fatal("bucket exists?")
	}
	b, err := c.BucketByName(name, true)
	if err != nil {
		t.Fatal(err)
	}
	buckets, err := c.Buckets()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, bb := range buckets {
		if bb.Name == name {
			found = true
			if bb.ID != b.ID {
				t.Fatal("Bucket ID mismatch:", b.ID, bb.ID)
			}
			if bb.Type != "allPrivate" {
				t.Fatal("Bucket type mismatch:", bb.Type)
			}
		}
	}
	if !found {
		t.Fatal("Bucket did not appear in Buckets()")
	}

	if err := b.Delete(); err != nil {
		t.Fatal(err)
	}

	if _, err := c.BucketByName(name, false); err == nil {
		t.Fatal("Bucket did not disappear")
	}
}
