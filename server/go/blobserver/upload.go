package main

import (
	"camli/blobref"
	"camli/httputil"
	"fmt"
	"http"
	"io"
	"io/ioutil"
	"log"
	"os"
	)

type receivedBlob struct {
	blobRef  blobref.BlobRef
	size     int64
}

func handleMultiPartUpload(conn http.ResponseWriter, req *http.Request) {
	if !(req.Method == "POST" && req.URL.Path == "/camli/upload") {
		httputil.BadRequestError(conn, "Inconfigured handler.")
		return
	}

	receivedBlobs := make([]*receivedBlob, 0, 10)

	multipart, err := req.MultipartReader()
	if multipart == nil {
		httputil.BadRequestError(conn, fmt.Sprintf(
			"Expected multipart/form-data POST request; %v", err))
		return
	}

	for {
		part, err := multipart.NextPart()
		if err != nil {
			log.Println("Error reading multipart section:", err)
			break
		}
		if part == nil {
			break
		}
		formName := part.FormName()
		log.Printf("New value [%s], part=%v\n", formName, part)

		ref := blobref.Parse(formName)
		if ref == nil {
			log.Printf("Ignoring form key [%s]\n", formName)
			continue
		}

		blobGot, err := receiveBlob(ref, part)
		if err != nil {
			log.Printf("Error receiving blob %v: %v\n", ref, err)
			break
		}
		log.Printf("Received blob %v\n", blobGot)
		receivedBlobs = append(receivedBlobs, blobGot)
	}

	log.Println("Done reading multipart body.")
	ret := commonUploadResponse(req)

	received := make([]map[string]interface{}, 0)
	for _, got := range receivedBlobs {
		log.Printf("Got blob: %v\n", got)
		blob := make(map[string]interface{})
		blob["blobRef"] = got.blobRef.String()
		blob["size"] = got.size
		received = append(received, blob)
	}
	ret["received"] = received

	httputil.ReturnJson(conn, ret)
}

func commonUploadResponse(req *http.Request) map[string]interface{} {
	ret := make(map[string]interface{})
	ret["maxUploadSize"] = 2147483647  // 2GB.. *shrug*
	ret["uploadUrlExpirationSeconds"] = 86400
	if len(req.Host) > 0 {
		scheme := "http" // TODO: https
		ret["uploadUrl"] = fmt.Sprintf("%s://%s/camli/upload",
			scheme, req.Host)
	} else {
		ret["uploadUrl"] = "/camli/upload"
	}
	return ret
}

func receiveBlob(blobRef blobref.BlobRef, source io.Reader) (blobGot *receivedBlob, err os.Error) {
	hashedDirectory := BlobDirectoryName(blobRef)
	err = os.MkdirAll(hashedDirectory, 0700)
	if err != nil {
		return
	}

	var tempFile *os.File
	tempFile, err = ioutil.TempFile(hashedDirectory, BlobFileBaseName(blobRef)+".tmp")
	if err != nil {
		return
	}

	success := false // set true later
	defer func() {
		if !success {
			log.Println("Removing temp file: ", tempFile.Name())
			os.Remove(tempFile.Name())
		}
	}()

	hash := blobRef.Hash()
	var written int64
	written, err = io.Copy(io.MultiWriter(hash, tempFile), source)
	if err != nil {
		return
	}
	if err = tempFile.Close(); err != nil {
		return
	}

	fileName := BlobFileName(blobRef)
	if err = os.Rename(tempFile.Name(), fileName); err != nil {
		return
	}

	stat, err := os.Lstat(fileName)
	if err != nil {
		return
	}
	if !stat.IsRegular() || stat.Size != written {
		err = os.NewError("Written size didn't match.")
		return
	}

	blobGot = &receivedBlob{blobRef: blobRef, size: stat.Size}
	success = true
	return
}

func handlePut(conn http.ResponseWriter, req *http.Request) {
	blobRef := BlobFromUrlPath(req.URL.Path)
	if blobRef == nil {
		httputil.BadRequestError(conn, "Malformed PUT URL.")
		return
	}

	if !blobRef.IsSupported() {
		httputil.BadRequestError(conn, "unsupported object hash function")
		return
	}

	_, err := receiveBlob(blobRef, req.Body)
	if err != nil {
		httputil.ServerError(conn, err)
		return
	}

	fmt.Fprint(conn, "OK")
}

