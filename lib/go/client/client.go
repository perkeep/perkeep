package client

import (
	"fmt"
	"log"
	"os"
	"sync"
)

type Stats struct {
	// The number of uploads that were requested, but perhaps
	// not actually performed if the server already had the items.
	UploadRequests  ByCountAndBytes

	// The uploads which were actually sent to the blobserver
	// due to the server not having the blobs
	Uploads         ByCountAndBytes
}

func (s *Stats) String() string {
	return "[uploadRequests=" + s.UploadRequests.String() + " uploads=" + s.Uploads.String() + "]"
}

type Client struct {
	server   string
	password string
	
	statsMutex  sync.Mutex
	stats      Stats

	log       *log.Logger  // not nil
}

type ByCountAndBytes struct {
	Blobs int
	Bytes int64
}

func (bb *ByCountAndBytes) String() string {
	return fmt.Sprintf("[blobs=%d bytes=%d]", bb.Blobs, bb.Bytes)
}

func NewOrFail() *Client {
	log := log.New(os.Stderr, "", log.Ldate|log.Ltime)
	return &Client{server: blobServerOrDie(), password: passwordOrDie(), log: log}
}

type devNullWriter struct{}
func (_ *devNullWriter) Write(p []byte) (int, os.Error) {
	return len(p), nil
}

func (c *Client) SetLogger(logger *log.Logger) {
	if logger == nil {
		c.log = log.New(&devNullWriter{}, "", 0)
	} else {
		c.log = logger
	}
}

func (c *Client) Stats() Stats {
	c.statsMutex.Lock()
	defer c.statsMutex.Unlock()
	return c.stats  // copy
}

func (c *Client) authHeader() string {
	return "Basic " + encodeBase64("username:" + c.password)
}
