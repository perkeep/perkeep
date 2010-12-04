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
	"bytes"
	"crypto/openpgp/armor"
	"crypto/openpgp/packet"
///	"crypto/rsa"
//	"crypto/sha1"
	"camli/blobref"
	"fmt"
	"io/ioutil"
	"json"
	"log"
	"os"
	"flag"
	"camli/http_util"
	"http"
	)

const sigSeparator = `,"camliSig":"`

var flagPubKeyDir *string = flag.String("pubkey-dir", "test/pubkey-blobs",
	"Temporary development hack; directory to dig-xxxx.camli public keys.")

func openArmoredPublicKeyFile(fileName string) (*packet.PublicKeyPacket, os.Error) {
	data, err := ioutil.ReadFile(fileName)
	if err != nil {
		return nil, os.NewError(fmt.Sprintf("Error reading public key file: %v", err))
	}

	block, _ := armor.Decode(data)
	if block == nil {
		return nil, os.NewError("Couldn't find PGP block in public key file")
	}
	if block.Type != "PGP PUBLIC KEY BLOCK" {
		return nil, os.NewError("Invalid public key blob.")
	}
	buf := bytes.NewBuffer(block.Bytes)
	p, err := packet.ReadPacket(buf)
	if err != nil {
		return nil, os.NewError(fmt.Sprintf("Invalid public key blob: %v", err))
	}

	pk, ok := p.(packet.PublicKeyPacket)
	if !ok {
		return nil, os.NewError(fmt.Sprintf("Invalid public key blob; not a public key packet"))
	}
	return &pk, nil
}

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

	// See doc/json-signing/* for background and details
	// on these variable names.

	BA := []byte(sjson)
	sigIndex := bytes.LastIndex(BA, []byte(sigSeparator))
	if sigIndex == -1 {
		verifyFail("no 13-byte camliSig separator found in sjson")
		return
	}

	// "Bytes Payload"
	BP := BA[0:sigIndex]

	// "Bytes Payload JSON".  Note we re-use the memory (the ",")
	// from BA in BPJ, so we can't re-use that "," byte for
	// the opening "{" in "BS".
	BPJ := BA[0:sigIndex+1]
	BPJ[sigIndex] = '}'

	BS := []byte("{" + sjson[sigIndex+1:])

	log.Printf("BP = [%s]", string(BP))
	log.Printf("BPJ = [%s]", string(BPJ))
	log.Printf("BS = [%s]", string(BS))
	
	sjsonKeys := make(map[string]interface{})
	if err := json.Unmarshal(BPJ, &sjsonKeys); err != nil {
		verifyFail("parse error; JSON is invalid")
		return
	}
	log.Printf("Got json: %v", sjsonKeys)

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
	
	sigKeyMap := make(map[string]interface{})
	if err := json.Unmarshal(BS, &sigKeyMap); err != nil {
		verifyFail("parse error; signature JSON invalid")
		return
	}
	log.Printf("Got sigKeyMap: %v", sigKeyMap)
	if len(sigKeyMap) != 1 {
		verifyFail("signature JSON didn't have exactly 1 key")
		return
	}
	sigVal, hasCamliSig := sigKeyMap["camliSig"]
	if !hasCamliSig {
		verifyFail("no 'camliSig' key in signature")
		return
	}

	log.Printf("sigValu = [%s]", sigVal)

	// verify that we have the public key signature file
	publicKeyFile := fmt.Sprintf("%s/%s.camli", *flagPubKeyDir, signerBlob.String())

	pk, err := openArmoredPublicKeyFile(publicKeyFile)
	if err != nil {
		verifyFail(fmt.Sprintf("Error opening public key file: %v", err))
		return
	}
	log.Printf("Public key packet: %v", pk)

	log.Printf("TODO: finish implementing")
	conn.WriteHeader(http.StatusNotImplemented)
	conn.Write([]byte("TODO: implement"))
}
