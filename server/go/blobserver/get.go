package main

import (
	"camli/blobref"
	"camli/httputil"
	"fmt"
	"http"
	"os"
	"io"
)

func createGetHandler(fetcher blobref.Fetcher) func (http.ResponseWriter, *http.Request) {
	return func (conn http.ResponseWriter, req *http.Request) {
		handleGet(conn, req, fetcher);
	}
}

func handleGet(conn http.ResponseWriter, req *http.Request, fetcher blobref.Fetcher) {
	blobRef := BlobFromUrlPath(req.URL.Path)
	if blobRef == nil {
		httputil.BadRequestError(conn, "Malformed GET URL.")
		return
	}
	file, size, err := fetcher.Fetch(blobRef)
	switch err {
	case nil:
		break
	case os.ENOENT:
		conn.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(conn, "Object not found.")
		return
	default:
		httputil.ServerError(conn, err)
		return
	}

	defer file.Close()

	reqRange := getRequestedRange(req)
	if reqRange.SkipBytes != 0 {
		_, err = file.Seek(reqRange.SkipBytes, 0)
		if err != nil {
			httputil.ServerError(conn, err)
			return
		}
	}

	var input io.Reader = file
	if reqRange.LimitBytes != -1 {
		input = io.LimitReader(file, reqRange.LimitBytes)
	}

	remainBytes := size - reqRange.SkipBytes
	if reqRange.LimitBytes != -1 &&
		reqRange.LimitBytes < remainBytes {
		remainBytes = reqRange.LimitBytes
	}

	conn.SetHeader("Content-Type", "application/octet-stream")
	if !reqRange.IsWholeFile() {
		conn.SetHeader("Content-Range",
			fmt.Sprintf("bytes %d-%d/%d", reqRange.SkipBytes,
			reqRange.SkipBytes + remainBytes,
			size))
		conn.WriteHeader(http.StatusPartialContent)
	}
	bytesCopied, err := io.Copy(conn, input)

	// If there's an error at this point, it's too late to tell the client,
	// as they've already been receiving bytes.  But they should be smart enough
	// to verify the digest doesn't match.  But we close the (chunked) response anyway,
	// to further signal errors.
	killConnection := func() {
		closer, _, err := conn.Hijack()
		if err != nil {
			closer.Close()
		}
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error sending file: %v, err=%v\n", blobRef, err)
		killConnection()
		return
	}
	if bytesCopied != remainBytes {
		fmt.Fprintf(os.Stderr, "Error sending file: %v, copied=%d, not %d\n", blobRef,
			bytesCopied, remainBytes)
		killConnection()
		return
	}
}
