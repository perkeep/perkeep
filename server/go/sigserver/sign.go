package main

import (
	"http"
	"http_util"
)

func handleSign(conn http.ResponseWriter, req *http.Request) {
	if !(req.Method == "POST" && req.URL.Path == "/camli/sig/sign") {
		http_util.BadRequestError(conn, "Inconfigured handler.")
		return
	}

	req.ParseForm()

	json := req.FormValue("json")
	if json == "" {
		http_util.BadRequestError(conn, "No json parameter")
		return
	}

	keyId := req.FormValue("keyid")
	if keyId == "" {
		http_util.BadRequestError(conn, "No keyid parameter")
		return
	}
}
