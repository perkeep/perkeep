package main

import (
	"camli/httputil"
	"camli/jsonsign"
	"fmt"
	"http"
)

const kMaxJsonLength = 1024 * 1024

func handleSign(conn http.ResponseWriter, req *http.Request) {
	if !(req.Method == "POST" && req.URL.Path == "/camli/sig/sign") {
		httputil.BadRequestError(conn, "Inconfigured handler.")
		return
	}

	req.ParseForm()

	jsonStr := req.FormValue("json")
	if jsonStr == "" {
		httputil.BadRequestError(conn, "Missing json parameter")
		return
	}
	if len(jsonStr) > kMaxJsonLength {
		httputil.BadRequestError(conn, "json parameter too large")
		return
	}

	sreq := &jsonsign.SignRequest{UnsignedJson: jsonStr, Fetcher: blobFetcher}
	signedJson, err := sreq.Sign()
	if err != nil {
		// TODO: some aren't really a "bad request"
		httputil.BadRequestError(conn, fmt.Sprintf("%v", err))
		return
	}
	conn.Write([]byte(signedJson))
}
