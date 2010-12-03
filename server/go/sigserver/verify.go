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
//	"bytes"
//	"crypto/openpgp/armor"
//	"crypto/openpgp/packet"
///	"crypto/rsa"
//	"crypto/sha1"
//	"io/ioutil"
	"camli/blobref"
	"json"
	"log"
	"flag"
	"camli/http_util"
	"http"
	)

var flagPubKeyDir *string = flag.String("pubkey-dir", "test/pubkey-blobs",
	"Temporary development hack; directory to dig-xxxx.camli public keys.")

func handleVerify(conn http.ResponseWriter, req *http.Request) {
	if !(req.Method == "POST" && req.URL.Path == "/camli/sig/verify") {
		http_util.BadRequestError(conn, "Inconfigured handler.")
		return
	}

	verifyFail := func(msg string) {
		conn.WriteHeader(http.StatusOK)  // no HTTP response code fun, error's in JSON
		m := make(map[string]interface{})
		m["signatureValid"] = 0
		m["errorMessage"] = msg
		http_util.ReturnJson(conn, m)
	}

	req.ParseForm()

	sjson := req.FormValue("sjson")
	if sjson == "" {
		http_util.BadRequestError(conn, "Missing sjson parameter.")
		return
	}
	
	sjsonKeys := make(map[string]interface{})
	if err := json.Unmarshal([]byte(sjson), &sjsonKeys); err != nil {
		verifyFail("parse error; JSON is invalid")
		return
	}

	if _, hasVersion := sjsonKeys["camliVersion"]; !hasVersion {
		verifyFail("Missing 'camliVersion' in the JSON payload")
                return
	}

	signer, hasSigner := sjsonKeys["camliSigner"]
	if !hasSigner {
		verifyFail("Missing 'camliSigner' in the JSON payload")
		return
	}

	if _, ok := signer.(string); !ok {
		verifyFail("Invalid 'camliSigner' in the JSON payload")
		return
	}

	signerBlob := blobref.Parse(signer.(string))
	if signerBlob == nil {
		verifyFail("Malformed 'camliSigner' blobref in the JSON payload")
		return
	}
	log.Printf("Signer: %v", signerBlob)
	
	log.Printf("Got json: %v", sjsonKeys)
	conn.WriteHeader(http.StatusNotImplemented)
	conn.Write([]byte("TODO: implement"))
}
