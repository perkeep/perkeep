// Copyright 2010 Brad Fitzpatrick <brad@danga.com>
//
// See LICENSE.

package main

import (
	"bytes"
	"camli/clientconfig"
	"camli/http"
	"crypto/sha1"
	"flag"
	"fmt"
	"io"
	"json"
	"log"
	"os"
	"strings"
)


// Things that can be uploaded.  (at most one of these)
var flagBlob *bool = flag.Bool("blob", true, "upload a file's bytes as a single blob")
var flagFile *bool = flag.Bool("file", false, "upload a file's bytes as a blob, as well as its JSON file record")

type UploadHandle struct {
	blobref  string
	contents io.ReadSeeker
}

// Upload agent
type Agent struct {
	server   string
	password string
}

func NewAgent(server, password string) *Agent {
	return &Agent{server, password}
}

func (a *Agent) Upload(h *UploadHandle) {
	url := fmt.Sprintf("%s/camli/preupload", a.server)
	fmt.Println("Need to upload: ", h, "to", url)

	error := func(msg string, e os.Error) {
		fmt.Fprintf(os.Stderr, "%s on %v: %v\n", msg, h.blobref, e)
		return
	}

	resp, err := http.Post(
		url,
		"application/x-www-form-urlencoded",
		strings.NewReader("camliversion=1&blob1="+h.blobref))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Upload error for %v: %v\n",
			h.blobref, err)
	}

	fmt.Println("Got response:", resp)
	buf := new(bytes.Buffer)
	io.Copy(buf, resp.Body)
	resp.Body.Close()

	pur := make(map[string]interface{})
	jerr := json.Unmarshal(buf.Bytes(), &pur)
	if jerr != nil {
		error("preupload parse error", jerr)
		return
	}

	uploadUrl, ok := pur["uploadUrl"].(string)
	if uploadUrl == "" {
		error("no uploadUrl in preupload response", nil)
		return
	}

	alreadyHave, ok := pur["alreadyHave"].([]interface{})
	if !ok {
		error("no alreadyHave array in preupload response", nil)
		return
	}

	for _, haveObj := range alreadyHave {
		haveObj := haveObj.(map[string]interface{})
		if haveObj["blobRef"].(string) == h.blobref {
			fmt.Println("already have it!")
			// TODO: signal success
			return
		}
	}

	fmt.Println("preupload done:", pur, alreadyHave)

	boundary := "sdf8sd8f7s9df9s7df9sd7sdf9s879vs7d8v7sd8v7sd8v"
	h.contents.Seek(0, 0)

	resp, err = http.Post(uploadUrl,
		"multipart/form-data; boundary="+boundary,
		io.MultiReader(
			strings.NewReader(fmt.Sprintf(
				"--%s\r\nContent-Disposition: form-data; name=\"%s\"\r\n\r\n",
				boundary,
				h.blobref)),
			h.contents,
			strings.NewReader("\r\n--"+boundary+"--\r\n")))

	if err != nil {
		error("camli upload error", err)
		return
	}
	fmt.Println("Uploaded!")
	fmt.Println("Got response: ", resp)
	resp.Write(os.Stdout)
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

func (a *Agent) UploadFileName(filename string) os.Error {
	log.Printf("Uploading filename: %s", filename)
	file, err := os.Open(filename, os.O_RDONLY, 0)
	if err != nil {
		return err
	}

	log.Printf("blob is:", blobName(file))
	handle := &UploadHandle{blobName(file), file}
	a.Upload(handle)
	return nil
}

func sumSet(flags ...*bool) (count int) {
	for _, f := range flags {
		if *f {
			count++
		}
	}
	return
}

func usage(msg string) {
	if msg != "" {
		fmt.Println("Error:", msg)
	}
	fmt.Println(`
Usage: camliup

  camliup --blob <filename(s) to upload as blobs>
  camliup --file <filename(s) to upload as blobs + JSON metadata>
`)
	flag.PrintDefaults()
	os.Exit(1)
}

func main() {
	flag.Parse()

	if sumSet(flagFile, flagBlob) != 1 {
		usage("Exactly one of --blob and --file may be set")
	}

	agent := NewAgent(clientconfig.BlobServerOrDie(), clientconfig.PasswordOrDie())
	if *flagFile || *flagBlob {
		for n := 0; n < flag.NArg(); n++ {
			agent.UploadFileName(flag.Arg(n))
		}
	}

	stats := agent.Wait()
	fmt.Println("Done uploading; stats:", stats)
}
