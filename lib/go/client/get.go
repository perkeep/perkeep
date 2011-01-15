package client

import (
	"camli/blobref"
	"camli/http"
	"fmt"
	"io"
	"os"
	"strconv"
)

func (c *Client) Fetch(b *blobref.BlobRef) (blobref.ReadSeekCloser, int64, os.Error) {
	url := fmt.Sprintf("%s/camli/%s", c.server, b)

	req := http.NewGetRequest(url)
	req.Header["Authorization"] = c.authHeader()
	resp, err := req.Send()
	if err != nil {
		return nil, 0, err
	}

	var size int64
	if s := resp.GetHeader("Content-Length"); s != "" {
		size, _ = strconv.Atoi64(s)
	}

	return nopSeeker{resp.Body}, size, nil
}

type nopSeeker struct {
	io.ReadCloser
}

func (n nopSeeker) Seek(offset int64, whence int) (ret int64, err os.Error) {
	return 0, os.NewError("seek unsupported")
}

