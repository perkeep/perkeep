// Copyright 2010 Brad Fitzpatrick <brad@danga.com>
//
// See LICENSE.

package main

import (
	"container/vector"
	"crypto/sha1"
	"encoding/base64"
	"flag"
	"fmt"
	"http"
	"io"
	"io/ioutil"
	"json"
	"os"
	"regexp"
	"strings"
)

// For `make`:
//import "./util/_obj/util"
// For `gofr`:
import "util/util"

// import "mime/multipart"
// import multipart "github.com/bradfitz/golang-mime-multipart"

var listen *string = flag.String("listen", "0.0.0.0:3179", "host:port to listen on")
var storageRoot *string = flag.String("root", "/tmp/camliroot", "Root directory to store files")
var stealthMode *bool = flag.Bool("stealth", true, "Run in stealth mode.")

var accessPassword string

var kBasicAuthPattern *regexp.Regexp = regexp.MustCompile(`^Basic ([a-zA-Z0-9\+/=]+)`)

func badRequestError(conn *http.Conn, errorMessage string) {
	conn.WriteHeader(http.StatusBadRequest)
	fmt.Fprintf(conn, "%s\n", errorMessage)
}

func serverError(conn *http.Conn, err os.Error) {
	conn.WriteHeader(http.StatusInternalServerError)
	fmt.Fprintf(conn, "Server error: %s\n", err)
}

func putAllowed(req *http.Request) bool {
	return isAuthorized(req)
}

func isAuthorized(req *http.Request) bool {
	auth, present := req.Header["Authorization"]
	if !present {
		return false
	}
	matches := kBasicAuthPattern.MatchStrings(auth)
	if len(matches) != 2 {
		return false
	}
	encoded := matches[1]
	enc := base64.StdEncoding
	decBuf := make([]byte, enc.DecodedLen(len(encoded)))
	n, err := enc.Decode(decBuf, []byte(encoded))
	if err != nil {
		return false
	}
	userpass := strings.Split(string(decBuf[0:n]), ":", 2)
	if len(userpass) != 2 {
		fmt.Println("didn't get two pieces")
		return false
	}
	password := userpass[1] // username at index 0 is currently unused
	return password != "" && password == accessPassword
}

func getAllowed(req *http.Request) bool {
	// For now...
	return putAllowed(req)
}

func handleCamliForm(conn *http.Conn, req *http.Request) {
	fmt.Fprintf(conn, `
<html>
<body>
<form method='POST' enctype="multipart/form-data" action="/camli/testform">
<input type="hidden" name="имя" value="брэд" />
Text unix: <input type="file" name="file-unix"><br>
Text win: <input type="file" name="file-win"><br>
Text mac: <input type="file" name="file-mac"><br>
Image png: <input type="file" name="image-png"><br>
<input type=submit>
</form>
</body>
</html>
`)
}

func returnJson(conn *http.Conn, data interface{}) {
	bytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		badRequestError(conn, fmt.Sprintf(
			"JSON serialization error: %v", err))
		return
	}
	conn.Write(bytes)
	conn.Write([]byte("\n"))
}

func requireAuth(handler func(conn *http.Conn, req *http.Request)) func (conn *http.Conn, req *http.Request) {
	return func (conn *http.Conn, req *http.Request) {
		if !isAuthorized(req) {
			conn.SetHeader("WWW-Authenticate", "Basic realm=\"camlistored\"")
			conn.WriteHeader(http.StatusUnauthorized)
			fmt.Fprintf(conn, "Authentication required.\n")
			return
		}
		handler(conn, req)
	}
}

func handleCamli(conn *http.Conn, req *http.Request) {
	handler := func (conn *http.Conn, req *http.Request) {
		badRequestError(conn, "Unsupported path or method.")
	}
	switch req.Method {
	case "GET":
		handler = requireAuth(handleGet)
	case "POST":
		switch req.URL.Path {
		case "/camli/preupload":
			handler = requireAuth(handlePreUpload)
		case "/camli/upload":
			handler = requireAuth(handleMultiPartUpload)
		case "/camli/testform": // debug only
			handler = handleTestForm
		case "/camli/form": // debug only
			handler = handleCamliForm
		}
	case "PUT": // no longer part of spec
		handler = requireAuth(handlePut)
	}
	handler(conn, req)
}

func handleGet(conn *http.Conn, req *http.Request) {
	if !getAllowed(req) {
		conn.SetHeader("WWW-Authenticate", "Basic realm=\"camlistored\"")
		conn.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintf(conn, "Authentication required.")
		return
	}

	blobRef := ParsePath(req.URL.Path)
	if blobRef == nil {
		badRequestError(conn, "Malformed GET URL.")
		return
	}
	fileName := blobRef.FileName()
	stat, err := os.Stat(fileName)
	if err == os.ENOENT {
		conn.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(conn, "Object not found.")
		return
	}
	if err != nil {
		serverError(conn, err)
		return
	}
	file, err := os.Open(fileName, os.O_RDONLY, 0)
	if err != nil {
		serverError(conn, err)
		return
	}
	conn.SetHeader("Content-Type", "application/octet-stream")
	bytesCopied, err := io.Copy(conn, file)

	// If there's an error at this point, it's too late to tell the client,
	// as they've already been receiving bytes.  But they should be smart enough
	// to verify the digest doesn't match.  But we close the (chunked) response anyway,
	// to further signal errors.
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error sending file: %v, err=%v\n", blobRef, err)
		closer, _, err := conn.Hijack()
		if err != nil {
			closer.Close()
		}
		return
	}
	if bytesCopied != stat.Size {
		fmt.Fprintf(os.Stderr, "Error sending file: %v, copied= %d, not %d%v\n", blobRef,
			bytesCopied, stat.Size)
		closer, _, err := conn.Hijack()
		if err != nil {
			closer.Close()
		}
		return
	}
}

func handleTestForm(conn *http.Conn, req *http.Request) {
	if !(req.Method == "POST" && req.URL.Path == "/camli/testform") {
		badRequestError(conn, "Inconfigured handler.")
		return
	}

	multipart, err := req.MultipartReader()
	if multipart == nil {
		badRequestError(conn, fmt.Sprintf("Expected multipart/form-data POST request; %v", err))
		return
	}

	for {
		part, err := multipart.NextPart()
		if err != nil {
			fmt.Println("Error reading:", err)
			break
		}
		if part == nil {
			break
		}
		formName := part.FormName()
		fmt.Printf("New value [%s], part=%v\n", formName, part)

		sha1 := sha1.New()
		io.Copy(sha1, part)
		fmt.Printf("Got part digest: %x\n", sha1.Sum())

	}
	fmt.Println("Done reading multipart body.")

}

func handlePreUpload(conn *http.Conn, req *http.Request) {
	if !(req.Method == "POST" && req.URL.Path == "/camli/preupload") {
		badRequestError(conn, "Inconfigured handler.")
		return
	}

	req.ParseForm()
	camliVersion := req.FormValue("camliversion")
	if camliVersion == "" {
		badRequestError(conn, "No camliversion")
		return
	}
	n := 0
	haveVector := new(vector.Vector)

	haveChan := make(chan *map[string]interface{})
	for {
		key := fmt.Sprintf("blob%v", n+1)
		value := req.FormValue(key)
		if value == "" {
			break
		}
		ref := ParseBlobRef(value)
		if ref == nil {
			badRequestError(conn, "Bogus blobref for key "+key)
			return
		}
		if !ref.IsSupported() {
			badRequestError(conn, "Unsupported or bogus blobref "+key)
		}
		n++

		// Parallel stat all the files...
		go func() {
			fi, err := os.Stat(ref.FileName())
			if err == nil && fi.IsRegular() {
				info := make(map[string]interface{})
				info["blobRef"] = ref.String()
				info["size"] = fi.Size
				haveChan <- &info
			} else {
				haveChan <- nil
			}
		}()
	}

	if n > 0 {
		for have := range haveChan {
			if have != nil {
				haveVector.Push(have)
			}
			n--
			if n == 0 {
				break
			}
		}
	}

	ret := make(map[string]interface{})
	ret["maxUploadSize"] = 2147483647 // 2GB.. *shrug*
	ret["alreadyHave"] = haveVector.Copy()
	ret["uploadUrl"] = "http://localhost:3179/camli/upload"
	ret["uploadUrlExpirationSeconds"] = 86400
	returnJson(conn, ret)
}

func handleMultiPartUpload(conn *http.Conn, req *http.Request) {
	if !(req.Method == "POST" && req.URL.Path == "/camli/upload") {
		badRequestError(conn, "Inconfigured handler.")
		return
	}

	if !putAllowed(req) {
		conn.SetHeader("WWW-Authenticate", "Basic realm=\"camlistored\"")
		conn.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintf(conn, "Authentication required.")
		return
	}

	multipart, err := req.MultipartReader()
	if multipart == nil {
		badRequestError(conn, fmt.Sprintf(
			"Expected multipart/form-data POST request; %v", err))
		return
	}

	for {
		part, err := multipart.NextPart()
		if err != nil {
			fmt.Println("Error reading multipart section:", err)
			break
		}
		if part == nil {
			break
		}
		formName := part.FormName()
		fmt.Printf("New value [%s], part=%v\n", formName, part)

		ref := ParseBlobRef(formName)
		if ref == nil {
			fmt.Printf("Ignoring form key [%s]\n", formName)
			continue
		}

		ok, err := receiveBlob(ref, part)
		if !ok {
			fmt.Printf("Error receiving blob %v: %v\n", ref, err)
		} else {
			fmt.Printf("Received blob %v\n", ref)
		}
	}
	fmt.Println("Done reading multipart body.")
}

func receiveBlob(blobRef *BlobRef, source io.Reader) (ok bool, err os.Error) {
	hashedDirectory := blobRef.DirectoryName()
	err = os.MkdirAll(hashedDirectory, 0700)
	if err != nil {
		return
	}

	var tempFile *os.File
	tempFile, err = ioutil.TempFile(hashedDirectory, blobRef.FileBaseName()+".tmp")
	if err != nil {
		return
	}

	success := false // set true later
	defer func() {
		if !success {
			fmt.Println("Removing temp file: ", tempFile.Name())
			os.Remove(tempFile.Name())
		}
	}()

	sha1 := sha1.New()
	var written int64
	written, err = io.Copy(util.NewTee(sha1, tempFile), source)
	if err != nil {
		return
	}
	if err = tempFile.Close(); err != nil {
		return
	}

	fileName := blobRef.FileName()
	if err = os.Rename(tempFile.Name(), fileName); err != nil {
		return
	}

	stat, err := os.Lstat(fileName)
	if err != nil {
		return
	}
	if !stat.IsRegular() || stat.Size != written {
		return false, os.NewError("Written size didn't match.")
	}

	success = true
	return true, nil
}

func handlePut(conn *http.Conn, req *http.Request) {
	blobRef := ParsePath(req.URL.Path)
	if blobRef == nil {
		badRequestError(conn, "Malformed PUT URL.")
		return
	}

	if !blobRef.IsSupported() {
		badRequestError(conn, "unsupported object hash function")
		return
	}

	if !putAllowed(req) {
		conn.SetHeader("WWW-Authenticate", "Basic realm=\"camlistored\"")
		conn.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintf(conn, "Authentication required.")
		return
	}

	_, err := receiveBlob(blobRef, req.Body)
	if err != nil {
		serverError(conn, err)
		return
	}

	fmt.Fprint(conn, "OK")
}

func HandleRoot(conn *http.Conn, req *http.Request) {
	if *stealthMode {
		fmt.Fprintf(conn, "Hi.\n")
	} else {
		fmt.Fprintf(conn, `
This is camlistored, a Camlistore storage daemon.
`)
	}
}

func main() {
	flag.Parse()

	accessPassword = os.Getenv("CAMLI_PASSWORD")
	if len(accessPassword) == 0 {
		fmt.Fprintf(os.Stderr,
			"No CAMLI_PASSWORD environment variable set.\n")
		os.Exit(1)
	}

	{
		fi, err := os.Stat(*storageRoot)
		if err != nil || !fi.IsDirectory() {
			fmt.Fprintf(os.Stderr,
				"Storage root '%s' doesn't exist or is not a directory.\n",
				*storageRoot)
			os.Exit(1)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", HandleRoot)
	mux.HandleFunc("/camli/", handleCamli)

	fmt.Printf("Starting to listen on http://%v/\n", *listen)
	err := http.ListenAndServe(*listen, mux)
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"Error in http server: %v\n", err)
		os.Exit(1)
	}
}
