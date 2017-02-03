package b2_test

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"strings"
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

var cleanup = flag.Bool("cleanup", false, "Delete all test-* buckets on start.")

func TestMain(m *testing.M) {
	flag.Parse()

	if *cleanup {
		c := getClient(nil)
		buckets, err := c.Buckets()
		if err != nil {
			log.Fatal(err)
		}
		for _, b := range buckets {
			if !strings.HasPrefix(b.Name, "test-") {
				continue
			}
			log.Println("Deleting bucket", b.Name)
			l := b.ListFilesVersions("", "")
			for l.Next() {
				fi := l.FileInfo()
				if err := c.DeleteFile(fi.ID, fi.Name); err != nil {
					log.Fatal(err)
				}
			}
			if err := l.Err(); err != nil {
				log.Fatal(err)
			}
			if err := b.Delete(); err != nil {
				log.Fatal(err)
			}
		}
	}

	os.Exit(m.Run())
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

func TestUnwrapError(t *testing.T) {
	c := getClient(t)

	_, err := c.GetFileInfoByID("jhgvcfgcgvhjhbjvghcf")
	if e, ok := b2.UnwrapError(err); !ok || e == nil {
		t.Fatalf("%T %v", err, err)
	}

	if err, ok := b2.UnwrapError(nil); ok || err != nil {
		t.Fatal(err)
	}

	if err, ok := b2.UnwrapError(errors.New("test")); ok || err != nil {
		t.Fatal(err)
	}
}
