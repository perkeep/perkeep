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

package jsonsign

import (
	"bytes"
	"crypto/openpgp"
	"flag"
	"fmt"
	"io"
	"json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"camli/blobref"
	"camli/misc/pinentry"
)

var _ = log.Printf

var gpgPath = "/usr/bin/gpg"
var flagSecretRing = ""

func AddFlags() {
	defSecRing := filepath.Join(os.Getenv("HOME"), ".gnupg", "secring.gpg")
	flag.StringVar(&gpgPath, "gpg-path", "/usr/bin/gpg", "Path to the gpg binary.")
	flag.StringVar(&flagSecretRing, "secret-keyring", defSecRing,
			"GnuPG secret keyring file to use.")
}

type SignRequest struct {
	UnsignedJson string
	Fetcher      interface{} // blobref.Fetcher or blobref.StreamingFetcher
	ServerMode   bool // if true, can't use pinentry or gpg-agent, etc.

	SecretKeyringPath string // Or "" to use flag value.
}

func (sr *SignRequest) secretRingPath() string {
	if sr.SecretKeyringPath != "" {
		return sr.SecretKeyringPath
	}
	return flagSecretRing
}

func (sr *SignRequest) Sign() (signedJson string, err os.Error) {
	trimmedJson := strings.TrimRightFunc(sr.UnsignedJson, unicode.IsSpace)

	// TODO: make sure these return different things
	inputfail := func(msg string) (string, os.Error) {
		return "", os.NewError(msg)
	}
	execfail := func(msg string) (string, os.Error) {
		return "", os.NewError(msg)
	}

	jmap := make(map[string]interface{})
	if err := json.Unmarshal([]byte(trimmedJson), &jmap); err != nil {
		return inputfail("json parse error")
	}

	camliSigner, hasSigner := jmap["camliSigner"]
	if !hasSigner {
		return inputfail("json lacks \"camliSigner\" key with public key blobref")
	}

	camliSignerStr, _ := camliSigner.(string)
	signerBlob := blobref.Parse(camliSignerStr)
	if signerBlob == nil {
		return inputfail("json \"camliSigner\" key is malformed or unsupported")
	}

	var pubkeyReader io.ReadCloser
	switch fetcher := sr.Fetcher.(type) {
	case blobref.Fetcher:
		pubkeyReader, _, err = fetcher.Fetch(signerBlob)
	case blobref.StreamingFetcher:
		pubkeyReader, _, err = fetcher.FetchStreaming(signerBlob)
	default:
		panic(fmt.Sprintf("jsonsign: bogus SignRequest.Fetcher of type %T", sr.Fetcher))
	}
	if err != nil {
		// TODO: not really either an inputfail or an execfail.. but going
		// with exec for now.
		return execfail(fmt.Sprintf("failed to find public key %s", signerBlob.String()))
	}

	pubk, err := openArmoredPublicKeyFile(pubkeyReader)
	if err != nil {
		return execfail(fmt.Sprintf("failed to parse public key from blobref %s: %v", signerBlob.String(), err))
	}

	// This check should be redundant if the above JSON parse succeeded, but
	// for explicitness...
	if len(trimmedJson) == 0 || trimmedJson[len(trimmedJson)-1] != '}' {
		return inputfail("json parameter lacks trailing '}'")
	}
	trimmedJson = trimmedJson[0 : len(trimmedJson)-1]

	// sign it
	secring, err := os.Open(sr.secretRingPath())
	if err != nil {
		return "", fmt.Errorf("jsonsign: failed to open secret ring file %q: %v", sr.secretRingPath(), err)
	}
	defer secring.Close()

	el, err := openpgp.ReadKeyRing(secring)
	if err != nil {
		return "", fmt.Errorf("jsonsign: openpgp.ReadKeyRing of %q: %v", sr.secretRingPath(), err)
	}
	var signer *openpgp.Entity
	for _, e := range el {
		if !bytes.Equal(e.PrivateKey.PublicKey.Fingerprint[:], pubk.Fingerprint[:]) {
			continue
		}
		signer = e
		break
	}

	if signer == nil {
		return "", fmt.Errorf("jsonsign: didn't find private key %q in %q", pubk.KeyIdShortString(), sr.secretRingPath())
	}

	if signer.PrivateKey.Encrypted {
		// TODO: syscall.Mlock a region and keep pass phrase in it.
		pinReq := &pinentry.Request{Prompt: "passphrase yo"}
		pin, err := pinReq.GetPIN()
		if err != nil {
			return "", fmt.Errorf("jsonsign: failed to get private key decryption password: %v", err)
		}
		err = signer.PrivateKey.Decrypt([]byte(pin))
		if err != nil {
			return "", fmt.Errorf("jsonsign: failed to decrypt private key: %v", err)
		}
	}

	var buf bytes.Buffer
	err = openpgp.ArmoredDetachSign(&buf, signer, strings.NewReader(trimmedJson))
	if err != nil {
		return "", err
	}

	output := buf.String()

	index1 := strings.Index(output, "\n\n")
	index2 := strings.Index(output, "\n-----")
	if index1 == -1 || index2 == -1 {
		return execfail("Failed to parse signature from gpg.")
	}
	inner := output[index1+2 : index2]
	signature := strings.Replace(inner, "\n", "", -1)

	return fmt.Sprintf("%s,\"camliSig\":\"%s\"}\n", trimmedJson, signature), nil
}
