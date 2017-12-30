// Copyright 2014 Tamás Gulácsi. All rights reserved.
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package picago

import (
	"bytes"
	"fmt"
	"html"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
)

/*
Upload photo
If userID is empty, "default" is used.
If albumID is empty, "default" is used.

fileName is the image's filename
summary is the caption of the photo.
MIME is the Content-Type, only support "image/bmp", "image/gif", "image/jpeg", and "image/png"
*/
func UploadPhoto(client *http.Client, userID, albumID, fileName, summary, MIME string, photoRaw []byte) (*Photo, error) {
	if userID == "" {
		userID = "default"
	}
	if albumID == "" {
		albumID = "default"
	}
	url := strings.Replace(photoURL, "{userID}", userID, 1)
	url = strings.Replace(url, "{albumID}", albumID, 1)
	url = url[0:strings.LastIndex(url, "?")]

	buf := bytes.NewBuffer(nil)
	w := multipart.NewWriter(buf)
	sw, _ := w.CreatePart(textproto.MIMEHeader{
		"Content-Type": []string{"application/atom+xml"},
	})
	fmt.Fprintf(sw,
		"<entry xmlns='http://www.w3.org/2005/Atom'><title>%s</title><summary>%s</summary><category scheme='http://schemas.google.com/g/2005#kind' term='http://schemas.google.com/photos/2007#photo'/></entry>\r\n",
		html.EscapeString(fileName),
		html.EscapeString(summary),
	)
	sw, _ = w.CreatePart(textproto.MIMEHeader{
		"Content-Type": []string{MIME},
	})
	sw.Write(photoRaw)
	w.Close()

	req, err := http.NewRequest(http.MethodPost, url, buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "multipart/related; boundary="+w.Boundary())
	req.Header.Set("Content-Length", strconv.Itoa(buf.Len()))
	req.Header.Set("MIME-version", "1.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		buf, _ := ioutil.ReadAll(resp.Body)
		return nil, fmt.Errorf("UploadPhoto(%s) got %s (%s)", url, resp.Status, buf)
	}

	var entry Entry
	if err := entry.DecodeReader(resp.Body); err != nil {
		return nil, err
	}
	photo, err := entry.photo()
	if err != nil {
		return nil, err
	}
	return &photo, nil
}
