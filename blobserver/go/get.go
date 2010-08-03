package main

import (
	"fmt"
	"http"
	"os"
	"io"
)

func handleGet(conn *http.Conn, req *http.Request) {
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

	reqRange := getRequestedRange(req)
	if reqRange.SkipBytes != 0 {
		_, err = file.Seek(reqRange.SkipBytes, 0)
		if err != nil {
			serverError(conn, err)
			return
		}
	}

	var input io.Reader = file
	if reqRange.LimitBytes != -1 {
		input = io.LimitReader(file, reqRange.LimitBytes)
	}

	conn.SetHeader("Content-Type", "application/octet-stream")
	if !reqRange.IsWholeFile() {
		remainBytes := stat.Size - reqRange.SkipBytes
		if reqRange.LimitBytes != -1 &&
			reqRange.LimitBytes < remainBytes {
			remainBytes = reqRange.LimitBytes
		}
		conn.SetHeader("Content-Range",
			fmt.Sprintf("%d-%d/%d", reqRange.SkipBytes,
			reqRange.SkipBytes + remainBytes,
			stat.Size))
	}
	bytesCopied, err := io.Copy(conn, input)

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
