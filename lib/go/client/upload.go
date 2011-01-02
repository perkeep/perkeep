package client

import (
	"bytes"
	"camli/blobref"
	"camli/http"
	"encoding/base64"
	"fmt"
	"io"
	"json"
	"log"
	"os"
	"strings"
)

type UploadHandle struct {
	BlobRef  *blobref.BlobRef
	Size     int64
	Contents io.Reader
}

type PutResult struct {
	BlobRef  *blobref.BlobRef
	Size     int64
	Skipped  bool    // already present on blobserver
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

func (c *Client) Upload(h *UploadHandle) (*PutResult, os.Error) {
	error := func(msg string, e os.Error) (*PutResult, os.Error) {
		err := os.NewError(fmt.Sprintf("Error uploading blob %s: %s; err=%s",
			h.BlobRef, msg, e))
		log.Print(err.String())
		return nil, err
	}

	c.statsMutex.Lock()
	c.stats.UploadRequests.Blobs++
	c.stats.UploadRequests.Bytes += h.Size
	c.statsMutex.Unlock()

	authHeader := "Basic " + encodeBase64("username:" + c.password)
	blobRefString := h.BlobRef.String()

	// Pre-upload.  Check whether the blob already exists on the
	// server and if not, the URL to upload it to.
	url := fmt.Sprintf("%s/camli/preupload", c.server)
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
					c.statsMutex.Lock()
					c.stats.Uploads.Blobs++
					c.stats.Uploads.Bytes += h.Size
					c.statsMutex.Unlock()
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
