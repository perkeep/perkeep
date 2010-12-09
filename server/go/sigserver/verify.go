package main

/*

  $ gpg --no-default-keyring --keyring=/tmp/foo --import --armor test/pubkey-blobs/sha1-82e6f3494f69

  $ gpg --no-default-keyring --keyring=/tmp/foo --verify  sig.tmp  doc.tmp ; echo $?
  gpg: Signature made Mon 29 Nov 2010 10:59:52 PM PST using RSA key ID 26F5ABDA
  gpg: Good signature from "Camli Tester <camli-test@example.com>"
  gpg: WARNING: This key is not certified with a trusted signature!
  gpg:          There is no indication that the signature belongs to the owner.
         Primary key fingerprint: FBB8 9AA3 20A2 806F E497  C049 2931 A67C 26F5 ABDA0

*/

import (
	"camli/httputil"
	"camli/jsonsign"
	"http"
	)

func handleVerify(conn http.ResponseWriter, req *http.Request) {
	if !(req.Method == "POST" && req.URL.Path == "/camli/sig/verify") {
		httputil.BadRequestError(conn, "Inconfigured handler.")
		return
	}

	req.ParseForm()
	sjson := req.FormValue("sjson")
	if sjson == "" {
		httputil.BadRequestError(conn, "Missing sjson parameter.")
		return
	}

	m := make(map[string]interface{})

	vreq := jsonsign.NewVerificationRequest(sjson, blobFetcher)
	if vreq.Verify() {
		m["signatureValid"] = 1
		m["verifiedData"] = vreq.PayloadMap
	} else {
		errStr := vreq.Err.String()
		m["signatureValid"] = 0
		m["errorMessage"] = errStr
	}

	conn.WriteHeader(http.StatusOK)  // no HTTP response code fun, error info in JSON
	httputil.ReturnJson(conn, m)
}
