/*
Copyright 2011 Google Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

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
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/jsonsign"
	"net/http"
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

	vreq := jsonsign.NewVerificationRequest(sjson, pubKeyFetcher)
	if vreq.Verify() {
		m["signatureValid"] = 1
		m["verifiedData"] = vreq.PayloadMap
	} else {
		m["signatureValid"] = 0
		m["errorMessage"] = vreq.Err.Error()
	}

	conn.WriteHeader(http.StatusOK) // no HTTP response code fun, error info in JSON
	httputil.ReturnJSON(conn, m)
}
