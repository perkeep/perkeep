// Copyright 2010 Brad Fitzpatrick <brad@danga.com>
//
// See LICENSE.

package main

import (
	"bytes"
	"camli/blobref"
	"camli/clientconfig"
	"camli/http"
	"crypto/sha1"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"json"
	"log"
	"os"
	"strings"
)

// Things that can be uploaded.  (at most one of these)
var flagBlob *bool = flag.Bool("blob", false, "upload a file's bytes as a single blob")
var flagFile *bool = flag.Bool("file", false, "upload a file's bytes as a blob, as well as its JSON file record")
var flagVerbose *bool = flag.Bool("verbose", false, "be verbose")

var wereErrors = false

type UploadHandle struct {
	BlobRef  *blobref.BlobRef
	Size     int64
	Contents io.ReadSeeker
}

type PutResult struct {
	BlobRef  *blobref.BlobRef
	Size     int64
	Skipped  bool    // already present on blobserver
}

// Upload agent
type Agent struct {
	server   string
	password string
}

func NewAgent(server, password string) *Agent {
	return &Agent{server, password}
}

func encodeBase64(s string) string {
	buf := make([]byte, base64.StdEncoding.EncodedLen(len(s)))
	base64.StdEncoding.Encode(buf, []byte(s))
	return string(buf)
}

func jsonFromResponse(resp *http.Response) (map[string]interface{}, os.Error) {
	if resp.StatusCode != 200 {
		return nil, os.NewError(fmt.Sprintf("HTTP response code is %d; no JSON to parse.", resp.StatusCode))
	}
	// TODO: LimitReader here for paranoia
	buf := new(bytes.Buffer)
	io.Copy(buf, resp.Body)
	resp.Body.Close()
	jmap := make(map[string]interface{})
	if jerr := json.Unmarshal(buf.Bytes(), &jmap); jerr != nil {
		return nil, jerr
	}
	return jmap, nil
}

func (a *Agent) Upload(h *UploadHandle) (*PutResult, os.Error) {
	error := func(msg string, e os.Error) (*PutResult, os.Error) {
		err := os.NewError(fmt.Sprintf("Error uploading blob %s: %s; err=%s",
			h.BlobRef, msg, e))
		log.Print(err.String())
		return nil, err
	}

	authHeader := "Basic " + encodeBase64("username:" + a.password)
	blobRefString := h.BlobRef.String()

	// Pre-upload.  Check whether the blob already exists on the
	// server and if not, the URL to upload it to.
	url := fmt.Sprintf("%s/camli/preupload", a.server)
	req := http.NewPostRequest(
		url,
		"application/x-www-form-urlencoded",
		strings.NewReader("camliversion=1&blob1="+blobRefString))
	req.Header["Authorization"] = authHeader

	resp, err := req.Send()
	if err != nil {
		return error("preupload http error", err)
	}

	pur, err := jsonFromResponse(resp)
	if err != nil {
		return error("preupload json parse error", err)
	}
	
	uploadUrl, ok := pur["uploadUrl"].(string)
	if uploadUrl == "" {
		return error("preupload json validity error: no 'uploadUrl'", nil)
	}

	alreadyHave, ok := pur["alreadyHave"].([]interface{})
	if !ok {
		return error("preupload json validity error: no 'alreadyHave'", nil)
	}

	pr := &PutResult{BlobRef: h.BlobRef, Size: h.Size}

	for _, haveObj := range alreadyHave {
		haveObj := haveObj.(map[string]interface{})
		if haveObj["blobRef"].(string) == h.BlobRef.String() {
			pr.Skipped = true
			return pr, nil
		}
	}

	boundary := "sdf8sd8f7s9df9s7df9sd7sdf9s879vs7d8v7sd8v7sd8v"
	h.Contents.Seek(0, 0)

	req = http.NewPostRequest(uploadUrl,
		"multipart/form-data; boundary="+boundary,
		io.MultiReader(
			strings.NewReader(fmt.Sprintf(
		                "--%s\r\nContent-Type: application/octet-stream\r\n" +
		                "Content-Disposition: form-data; name=\"%s\"; filename=\"%s\"\r\n\r\n",
				boundary,
				h.BlobRef, h.BlobRef)),
			h.Contents,
			strings.NewReader("\r\n--"+boundary+"--\r\n")))
	req.Header["Authorization"] = authHeader
	resp, err = req.Send()
	if err != nil {
		return error("upload http error", err)
	}

	// The only valid HTTP responses are 200 and 303.
	if resp.StatusCode != 200 && resp.StatusCode != 303 {
		return error(fmt.Sprintf("invalid http response %d in upload response", resp.StatusCode), nil)
	}

	if resp.StatusCode == 303 {
		// TODO
		log.Exitf("TODO: handle 303?  or does the Go http client do it already?  how to enforce only 200 and 303 if so?")
	}

	ures, err := jsonFromResponse(resp)
	if err != nil {
		return error("json parse from upload error", err)
	}

	errorText, ok := ures["errorText"].(string)
	if ok {
		log.Printf("Blob server reports error: %s", errorText)
	}

	received, ok := ures["received"].([]interface{})
	if !ok {
		return error("upload json validity error: no 'received'", nil)
	}

	for _, rit := range received {
		it, ok := rit.(map[string]interface{})
		if !ok {
			return error("upload json validity error: 'received' is malformed", nil)
		}
		if it["blobRef"] == blobRefString {
			switch size := it["size"].(type) {
			case nil:
				return error("upload json validity error: 'received' is missing 'size'", nil)
			case float64:
				if int64(size) == h.Size {
					// Success!
					return pr, nil
				} else {
					return error(fmt.Sprintf("Server got blob, but reports wrong length (%v; expected %d)",
						size, h.Size), nil)
				}
			default:
				return error("unsupported type of 'size' in received response", nil)
			}
		}
	}

	return nil, os.NewError("Server didn't receive blob.")
}

func blobDetails(contents io.ReadSeeker) (bref *blobref.BlobRef, size int64, err os.Error) {
	s1 := sha1.New()
	contents.Seek(0, 0)
	size, err = io.Copy(s1, contents)
	if err != nil {
		return
	}
	return blobref.Parse(fmt.Sprintf("sha1-%x", s1.Sum())), size, nil
}

func (a *Agent) UploadFileBlob(filename string) (*PutResult, os.Error) {
	if *flagVerbose {
		log.Printf("Uploading filename: %s", filename)
	}
	file, err := os.Open(filename, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}

	ref, size, err := blobDetails(file)
	if err != nil {
		return nil, err
	}
	handle := &UploadHandle{ref, size, file}
	return a.Upload(handle)
}

func (a *Agent) UploadFile(filename string) (*PutResult, os.Error) {
	// Put the blob of the file itself.  (TODO: smart boundary chunking)
	// For now we just store it as one range.
	blobpr, err := a.UploadFileBlob(filename)
	if err != nil {
		return nil, err
	}
	fmt.Println("Got blobref for file blob: ", blobpr.BlobRef.String())
	return nil, nil
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

func handleResult(what string, pr *PutResult, err os.Error) {
	if err != nil {
		log.Printf("Error putting %s: %s", what, err)
		wereErrors = true
		return
	}
	if *flagVerbose {
		fmt.Printf("Put %s: %q\n", what, pr)
	} else {
		fmt.Println(pr.BlobRef.String())
	}
}

func main() {
	flag.Parse()

	if sumSet(flagFile, flagBlob) != 1 {
		usage("Exactly one of --blob and --file may be set")
	}

	agent := NewAgent(clientconfig.BlobServerOrDie(), clientconfig.PasswordOrDie())
	if *flagFile || *flagBlob {
		for n := 0; n < flag.NArg(); n++ {
			if *flagBlob {
				pr, err := agent.UploadFileBlob(flag.Arg(n))
				handleResult("blob", pr, err)
			} else {
				pr, err := agent.UploadFile(flag.Arg(n))
				handleResult("file", pr, err)
			}
		}
	}

	if wereErrors {
		os.Exit(2)
	}
}
