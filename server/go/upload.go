package main

import (
	"http"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	)

type receivedBlob struct {
	blobRef  *BlobRef
	size     int64
}

func handleMultiPartUpload(conn http.ResponseWriter, req *http.Request) {
	if !(req.Method == "POST" && req.URL.Path == "/camli/upload") {
		badRequestError(conn, "Inconfigured handler.")
		return
	}

	receivedBlobs := make([]*receivedBlob, 0, 10)

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

		blobGot, err := receiveBlob(ref, part)
		if err != nil {
			fmt.Printf("Error receiving blob %v: %v\n", ref, err)
			break
		}
		fmt.Printf("Received blob %v\n", blobGot)
		receivedBlobs = append(receivedBlobs, blobGot)
	}

	fmt.Println("Done reading multipart body.")
	for _, got := range receivedBlobs {
		fmt.Printf("Got blob: %v\n", got)
	}
	// TODO: put the blobs in the JSON here

	ret := commonUploadResponse(req)
	returnJson(conn, ret)
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

func receiveBlob(blobRef *BlobRef, source io.Reader) (blobGot *receivedBlob, err os.Error) {
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

	hash := blobRef.Hash()
	var written int64
	written, err = io.Copy(io.MultiWriter(hash, tempFile), source)
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
		err = os.NewError("Written size didn't match.")
		return
	}

	blobGot = &receivedBlob{blobRef: blobRef, size: stat.Size}
	success = true
	return
}

func handlePut(conn http.ResponseWriter, req *http.Request) {
	blobRef := ParsePath(req.URL.Path)
	if blobRef == nil {
		badRequestError(conn, "Malformed PUT URL.")
		return
	}

	if !blobRef.IsSupported() {
		badRequestError(conn, "unsupported object hash function")
		return
	}

	_, err := receiveBlob(blobRef, req.Body)
	if err != nil {
		serverError(conn, err)
		return
	}

	fmt.Fprint(conn, "OK")
}

