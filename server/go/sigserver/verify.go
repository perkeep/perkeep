package main

/*

$ gpg --no-default-keyring --keyring=/tmp/foo --import --armor test/pubkey-blobs/sha1-82e6f3494f698aa498d5906349c0aa0a183d89a6.camli

$ gpg --no-default-keyring --keyring=/tmp/foo --verify  sig.tmp  doc.tmp ; echo $?
gpg: Signature made Mon 29 Nov 2010 10:59:52 PM PST using RSA key ID 26F5ABDA
gpg: Good signature from "Camli Tester <camli-test@example.com>"
gpg: WARNING: This key is not certified with a trusted signature!
gpg:          There is no indication that the signature belongs to the owner.
	Primary key fingerprint: FBB8 9AA3 20A2 806F E497  C049 2931 A67C 26F5 ABDA
0

*/

import (
	"camli/http_util"
	"http"
)

func handleVerify(conn http.ResponseWriter, req *http.Request) {
	if !(req.Method == "POST" && req.URL.Path == "/camli/sig/verify") {
		http_util.BadRequestError(conn, "Inconfigured handler.")
		return
	}

	req.ParseForm()

	json := req.FormValue("sjson")
	if json == "" {
		http_util.BadRequestError(conn, "Missing sjson parameter.")
		return
	}

	conn.WriteHeader(http.StatusNotImplemented)
	conn.Write([]byte("TODO: implement"))
}
