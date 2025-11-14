/*
Copyright 2011 The Perkeep Authors

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

package jsonsign

import (
	"bytes"
	"context"
	"crypto"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/camerrors"

	"golang.org/x/crypto/openpgp/armor"
	"golang.org/x/crypto/openpgp/packet"
)

const sigSeparator = `,"camliSig":"`

// reArmor takes a camliSig (single line armor) and turns it back into an PGP-style
// multi-line armored string
func reArmor(line string) string {
	lastEq := strings.LastIndex(line, "=")
	if lastEq == -1 {
		return ""
	}
	buf := new(bytes.Buffer)
	fmt.Fprintf(buf, "-----BEGIN PGP SIGNATURE-----\n\n")
	payload := line[0:lastEq]
	crc := line[lastEq:]
	for len(payload) > 0 {
		chunkLen := min(len(payload), 60)
		fmt.Fprintf(buf, "%s\n", payload[0:chunkLen])
		payload = payload[chunkLen:]
	}
	fmt.Fprintf(buf, "%s\n-----END PGP SIGNATURE-----\n", crc)
	return buf.String()
}

// See doc/json-signing/* for background and details
// on these variable names.
type VerifyRequest struct {
	fetcher blob.Fetcher // fetcher used to find public key blob

	ba  []byte // "bytes all"
	bp  []byte // "bytes payload" (the part that is signed)
	bpj []byte // "bytes payload, JSON" (BP + "}")
	bs  []byte // "bytes signature", "{" + separator + camliSig, valid JSON

	CamliSigner     blob.Ref
	CamliSig        string
	PublicKeyPacket *packet.PublicKey

	// set if Verify() returns true:
	PayloadMap  map[string]any // The JSON values from BPJ
	SignerKeyId string         // e.g. "2931A67C26F5ABDA"

	Err error // last error encountered
}

func (vr *VerifyRequest) fail(msg string) bool {
	vr.Err = errors.New("jsonsign: " + msg)
	return false
}

func (vr *VerifyRequest) ParseSigMap() bool {
	sigMap := make(map[string]any)
	if err := json.Unmarshal(vr.bs, &sigMap); err != nil {
		return vr.fail("invalid JSON in signature")
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
	vr.PayloadMap = make(map[string]any)
	pm := vr.PayloadMap

	if err := json.Unmarshal(vr.bpj, &pm); err != nil {
		return vr.fail("parse error; payload JSON is invalid")
	}

	if _, hasVersion := pm["camliVersion"]; !hasVersion {
		return vr.fail("missing 'camliVersion' in the JSON payload")
	}

	signer, hasSigner := pm["camliSigner"]
	if !hasSigner {
		return vr.fail("missing 'camliSigner' in the JSON payload")
	}

	if _, ok := signer.(string); !ok {
		return vr.fail("invalid 'camliSigner' in the JSON payload")
	}

	var ok bool
	vr.CamliSigner, ok = blob.Parse(signer.(string))
	if !ok {
		return vr.fail("malformed 'camliSigner' blobref in the JSON payload")
	}
	return true
}

func (vr *VerifyRequest) FindAndParsePublicKeyBlob(ctx context.Context) error {
	reader, _, err := vr.fetcher.Fetch(ctx, vr.CamliSigner)
	if err != nil {
		if err == os.ErrNotExist {
			return camerrors.ErrMissingKeyBlob
		}
		log.Printf("error fetching public key blob %v: %v", vr.CamliSigner, err)
		return err
	}
	defer reader.Close()
	pk, err := openArmoredPublicKeyFile(reader)
	if err != nil {
		return fmt.Errorf("error opening public key file: %v", err)
	}
	vr.PublicKeyPacket = pk
	return nil
}

func (vr *VerifyRequest) VerifySignature() bool {
	armorData := reArmor(vr.CamliSig)
	block, _ := armor.Decode(bytes.NewBufferString(armorData))
	if block == nil {
		return vr.fail("can't parse camliSig armor")
	}
	var p packet.Packet
	var err error
	p, err = packet.Read(block.Body)
	if err != nil {
		return vr.fail("error reading PGP packet from camliSig: " + err.Error())
	}
	sig, ok := p.(*packet.Signature)
	if !ok {
		return vr.fail("PGP packet isn't a signature packet")
	}
	if sig.Hash != crypto.SHA1 && sig.Hash != crypto.SHA256 {
		return vr.fail("I can only verify SHA1 or SHA256 signatures")
	}
	if sig.SigType != packet.SigTypeBinary {
		return vr.fail("I can only verify binary signatures")
	}
	hash := sig.Hash.New()
	hash.Write(vr.bp) // payload bytes
	err = vr.PublicKeyPacket.VerifySignature(hash, sig)
	if err != nil {
		return vr.fail(fmt.Sprintf("bad signature: %s", err))
	}
	vr.SignerKeyId = vr.PublicKeyPacket.KeyIdString()
	return true
}

func NewVerificationRequest(sjson string, fetcher blob.Fetcher) (vr *VerifyRequest) {
	if fetcher == nil {
		panic("NewVerificationRequest fetcher is nil")
	}
	vr = new(VerifyRequest)
	vr.ba = []byte(sjson)
	vr.fetcher = fetcher

	sigIndex := bytes.LastIndex(vr.ba, []byte(sigSeparator))
	if sigIndex == -1 {
		vr.Err = errors.New("jsonsign: no 13-byte camliSig separator found in sjson")
		return
	}

	// "Bytes Payload"
	vr.bp = vr.ba[:sigIndex]

	// "Bytes Payload JSON".  Note we re-use the memory (the ",")
	// from BA in BPJ, so we can't re-use that "," byte for
	// the opening "{" in "BS".
	vr.bpj = vr.ba[:sigIndex+1]
	vr.bpj[sigIndex] = '}'
	vr.bs = []byte("{" + sjson[sigIndex+1:])
	return
}

type VerifiedSignature struct {
	// TODO:
}

func (vr *VerifyRequest) Verify(ctx context.Context) (info VerifiedSignature, err error) {
	if vr.Err != nil {
		return VerifiedSignature{}, vr.Err
	}
	defer func() {
		if err != nil {
			// Don't allow callers to accidentally check this if it's not
			// valid.
			vr.PayloadMap = nil
			if vr.Err == nil {
				// The other functions should have filled this in
				// already, but just in case:
				vr.Err = err
			}
		}
	}()

	if !vr.ParseSigMap() {
		return VerifiedSignature{}, errors.New("parsing signature map failed")
	}
	if !vr.ParsePayloadMap() {
		return VerifiedSignature{}, errors.New("parsing payload map failed")
	}
	if err := vr.FindAndParsePublicKeyBlob(ctx); err != nil {
		return VerifiedSignature{}, err
	}
	if !vr.VerifySignature() {
		return VerifiedSignature{}, errors.New("signature verification failed")
	}

	return VerifiedSignature{
		// ...
	}, nil
}
