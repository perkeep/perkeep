// Copyright 2010 Brad Fitzpatrick <brad@danga.com>
//
// See LICENSE.

package main

import (
	"crypto/sha1"
	"flag"
	"fmt"
	"io"
	"os"
)

var flagFile *string = flag.String("file", "", "file to upload")
var flagServer *string = flag.String("server", "http://localhost:3179/", "camlistore server")

type UploadHandle struct {
	blobref  string
	contents io.ReadSeeker
}

// Upload agent
type Agent struct {
	server string
}

func NewAgent(server string) *Agent {
	return &Agent{server}
}

func (a *Agent) Upload(handle *UploadHandle) {
	// TODO
	fmt.Println("Need to upload: ", handle)
}

func (a *Agent) Wait() int {
	// TODO
	return 0
}

func blobName(contents io.ReadSeeker) string {
	s1 := sha1.New()
	contents.Seek(0, 0)
	io.Copy(s1, contents)
	return fmt.Sprintf("sha1-%x", s1.Sum())
}

func uploadFile(agent *Agent, filename string) os.Error {
	file, err := os.Open(filename, os.O_RDONLY, 0)
	if err != nil {
		return err
	}

	fmt.Println("blob is:", blobName(file))
	handle := &UploadHandle{blobName(file), file}
	agent.Upload(handle)
	return nil
}

func main() {
	flag.Parse()
	agent := NewAgent(*flagServer)
	if *flagFile != "" {
		uploadFile(agent, *flagFile)
	}
	
	stats := agent.Wait()
	fmt.Println("Done uploading; stats:", stats)
}
