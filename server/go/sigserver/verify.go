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

// See doc/json-signing/* for background and details
// on these variable names.
type VerifyRequest struct {
	ba  []byte  // "bytes all"
	bp  []byte  // "bytes payload" (the part that is signed)
	bpj []byte  // "bytes payload, JSON" (BP + "}")
	bs  []byte  // "bytes signature", "{" + separator + camliSig, valid JSON

	CamliSigner blobref.BlobRef
	PayloadMap  map[string]interface{}  // The JSON values from BPJ
	CamliSig    string

	PublicKeyPacket *packet.PublicKeyPacket

	Err os.Error   // last error encountered
}

func (vr *VerifyRequest) fail(msg string) bool {
	vr.Err = os.NewError(msg)
	return false
}

func (vr *VerifyRequest) ParseSigMap() bool {
	sigMap := make(map[string]interface{})
	if err := json.Unmarshal(vr.bs, &sigMap); err != nil {
		return vr.fail("Invalid JSON in signature")
	}

	if len(sigMap) != 1 {
		return vr.fail("signature JSON didn't have exactly 1 key")
	}

	sigVal, hasCamliSig := sigMap["camliSig"]
	if !hasCamliSig {
		return vr.fail("no 'camliSig' key in signature")
	}

	var ok bool
	vr.CamliSig, ok = sigVal.(string)
	if !ok {
		return vr.fail("camliSig not a string")
	}

	log.Printf("camliSig = [%s]", vr.CamliSig)
	return true
}

func (vr *VerifyRequest) ParsePayloadMap() bool {
	vr.PayloadMap = make(map[string]interface{})
	pm := vr.PayloadMap

	if err := json.Unmarshal(vr.bpj, &pm); err != nil {
		return vr.fail("parse error; payload JSON is invalid")
	}
	log.Printf("Got json: %v", pm)

	if _, hasVersion := pm["camliVersion"]; !hasVersion {
		return vr.fail("Missing 'camliVersion' in the JSON payload")
	}

	signer, hasSigner := pm["camliSigner"]
	if !hasSigner {
		return vr.fail("Missing 'camliSigner' in the JSON payload")
	}

	if _, ok := signer.(string); !ok {
		return vr.fail("Invalid 'camliSigner' in the JSON payload")
	}

	vr.CamliSigner = blobref.Parse(signer.(string))
	if vr.CamliSigner == nil {
		return vr.fail("Malformed 'camliSigner' blobref in the JSON payload")
	}

	log.Printf("Signer: %v", vr.CamliSigner)
	return true
}

func (vr *VerifyRequest) FindAndParsePublicKeyBlob() bool {
	// verify that we have the public key signature file
	publicKeyFile := fmt.Sprintf("%s/%s.camli", *flagPubKeyDir, vr.CamliSigner.String())
	pk, err := openArmoredPublicKeyFile(publicKeyFile)
	if err != nil {
		return vr.fail(fmt.Sprintf("Error opening public key file: %v", err))
	}
	log.Printf("Public key packet: %v", pk)
	vr.PublicKeyPacket = pk
	return true;
}

func (vr *VerifyRequest) VerifySignature() bool {
	 log.Printf("TODO: implement VerifySignature")
	return false
}

func NewVerificationRequest(sjson string) (vr *VerifyRequest) {
	vr = new(VerifyRequest)
	vr.ba = []byte(sjson)
	
	sigIndex := bytes.LastIndex(vr.ba, []byte(sigSeparator))
	if sigIndex == -1 {
		vr.Err = os.NewError("no 13-byte camliSig separator found in sjson")
		return
	}

	// "Bytes Payload"
	vr.bp = vr.ba[0:sigIndex]

	// "Bytes Payload JSON".  Note we re-use the memory (the ",")
	// from BA in BPJ, so we can't re-use that "," byte for
	// the opening "{" in "BS".
	vr.bpj = vr.ba[0:sigIndex+1]
	vr.bpj[sigIndex] = '}'
	vr.bs = []byte("{" + sjson[sigIndex+1:])

	log.Printf("BP = [%s]", string(vr.bp))
	log.Printf("BPJ = [%s]", string(vr.bpj))
	log.Printf("BS = [%s]", string(vr.bs))

	if !(vr.ParseSigMap() &&
		vr.ParsePayloadMap() &&
		vr.FindAndParsePublicKeyBlob() &&
		vr.VerifySignature()) {
		// Don't allow dumbs callers to accidentally check this
		// if it's not valid.
		vr.PayloadMap = nil
		if vr.Err == nil {
			// The other functions just fill this in already,
			// but just in case:
			vr.Err = os.NewError("Verification failed")
		}
		return
	}
	return
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

	vreq := NewVerificationRequest(sjson)
	log.Printf("Request is: %q", vreq)
	if vreq.Err != nil {
		verifyFail(vreq.Err.String())
		return
	}

	log.Printf("TODO: finish implementing")
	conn.WriteHeader(http.StatusNotImplemented)
	conn.Write([]byte("TODO: implement"))
}
