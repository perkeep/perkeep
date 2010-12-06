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
	"camli/blobref"
	"camli/httputil"
	"crypto/openpgp/armor"
	"crypto/openpgp/packet"
	"crypto/rsa"
	"crypto/sha1"
	"flag"
	"fmt"
	"http"
	"io/ioutil"
	"json"
	"log"
	"os"
	"strings"
	)

var logf = log.Printf

const sigSeparator = `,"camliSig":"`

var flagPubKeyDir *string = flag.String("pubkey-dir", "test/pubkey-blobs",
	"Temporary development hack; directory to dig-xxxx.camli public keys.")

// reArmor takes a camliSig (single line armor) and turns it back into an PGP-style
// multi-line armored string
func reArmor(line string) string {
	lastEq := strings.LastIndex(line, "=")
	if lastEq == -1 {
		return ""
	}
	return fmt.Sprintf(`
-----BEGIN PGP SIGNATURE-----

%s
%s
-----END PGP SIGNATURE-----
`, line[0:lastEq], line[lastEq:])
}

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

	return true
}

func (vr *VerifyRequest) ParsePayloadMap() bool {
	vr.PayloadMap = make(map[string]interface{})
	pm := vr.PayloadMap

	if err := json.Unmarshal(vr.bpj, &pm); err != nil {
		return vr.fail("parse error; payload JSON is invalid")
	}

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
	return true
}

func (vr *VerifyRequest) FindAndParsePublicKeyBlob() bool {
	// verify that we have the public key signature file
	publicKeyFile := fmt.Sprintf("%s/%s.camli", *flagPubKeyDir, vr.CamliSigner.String())
	pk, err := openArmoredPublicKeyFile(publicKeyFile)
	if err != nil {
		return vr.fail(fmt.Sprintf("Error opening public key file: %v", err))
	}
	vr.PublicKeyPacket = pk
	return true;
}

func (vr *VerifyRequest) VerifySignature() bool {
	armorData := reArmor(vr.CamliSig)
	block, _ := armor.Decode([]byte(armorData))
	if block == nil {
		return vr.fail("Can't parse camliSig armor")
	}
	buf := bytes.NewBuffer(block.Bytes)
	p, err := packet.ReadPacket(buf)
	if err != nil {
		return vr.fail("Error reading PGP packet from camliSig")
	}
	sig, ok := p.(packet.SignaturePacket)
	if !ok {
		return vr.fail("PGP packet isn't a signature packet")
	}
	if sig.Hash != packet.HashFuncSHA1 {
		return vr.fail("I can only verify SHA1 signatures")
	}
	if sig.SigType != packet.SigTypeBinary {
		return vr.fail("I can only verify binary signatures")
	}
	hash := sha1.New()
	hash.Write(vr.bp)  // payload bytes
	hash.Write(sig.HashSuffix)
	hashBytes := hash.Sum()
	if hashBytes[0] != sig.HashTag[0] || hashBytes[1] != sig.HashTag[1] {
		return vr.fail("hash tag doesn't match")
	}
	err = rsa.VerifyPKCS1v15(&vr.PublicKeyPacket.PublicKey, rsa.HashSHA1, hashBytes, sig.Signature)
	if err != nil {
		return vr.fail(fmt.Sprintf("bad signature: %s", err))
	}
	return true
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
	return
}

func (vr *VerifyRequest) Verify() bool {
	if vr.Err != nil {
		return false
	}

	if vr.ParseSigMap() &&
		vr.ParsePayloadMap() &&
		vr.FindAndParsePublicKeyBlob() &&
		vr.VerifySignature() {
		return true;
	}

	// Don't allow dumbs callers to accidentally check this
	// if it's not valid.
	vr.PayloadMap = nil
	if vr.Err == nil {
		// The other functions just fill this in already,
		// but just in case:
		vr.Err = os.NewError("Verification failed")
	}
	return false
}

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

	vreq := NewVerificationRequest(sjson)
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
