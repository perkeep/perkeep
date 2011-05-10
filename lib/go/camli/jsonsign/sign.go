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
	"exec"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"json"
	"log"
	"os"
	"strings"
	"unicode"

	"camli/blobref"
)

var gpgPath = "/usr/bin/gpg"
var flagRing = ""
var flagSecretRing = ""

func AddFlags() {
	flag.StringVar(&gpgPath, "gpg-path", "/usr/bin/gpg", "Path to the gpg binary.")
	flag.StringVar(&flagRing, "keyring", "./test/test-keyring.gpg",
			"GnuPG public keyring file to use.")
	flag.StringVar(&flagSecretRing, "secret-keyring", "./test/test-secring.gpg",
			"GnuPG secret keyring file to use.")
}

type SignRequest struct {
	UnsignedJson string
	Fetcher      interface{} // blobref.Fetcher or blobref.StreamingFetcher
	UseAgent     bool

	// In server-mode, don't use any default (user) keys
	// TODO: formalize what this means?
	ServerMode bool

	SecretKeyringPath string
	KeyringPath       string
}

func (sr *SignRequest) publicRingPath() string {
	if sr.KeyringPath != "" {
		return sr.KeyringPath
	}
	return flagRing
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

	pk, err := openArmoredPublicKeyFile(pubkeyReader)
	if err != nil {
		return execfail(fmt.Sprintf("failed to parse public key from blobref %s: %v", signerBlob.String(), err))
	}

	// This check should be redundant if the above JSON parse succeeded, but
	// for explicitness...
	if len(trimmedJson) == 0 || trimmedJson[len(trimmedJson)-1] != '}' {
		return inputfail("json parameter lacks trailing '}'")
	}
	trimmedJson = trimmedJson[0 : len(trimmedJson)-1]

	args := []string{"gpg",
		"--local-user", fmt.Sprintf("%X", pk.Fingerprint[len(pk.Fingerprint)-4:]),
		"--detach-sign",
		"--armor"}

	if sr.UseAgent {
		args = append(args, "--use-agent")
	}

	if sr.ServerMode {
		args = append(args,
			"--no-default-keyring",
			"--keyring", sr.publicRingPath(),
			"--secret-keyring", sr.secretRingPath())
	} else {
		override := false
		if kr := sr.publicRingPath(); kr != "" {
			args = append(args, "--keyring", kr)
			override = true
		}
		if kr := sr.secretRingPath(); kr != "" {
			args = append(args, "--secret-keyring", kr)
			override = true
		}
		if override {
			args = append(args, "--no-default-keyring")
		}
	}

	args = append(args, "-")

	cmd, err := exec.Run(
		gpgPath,
		args,
		os.Environ(),
		".",
		exec.Pipe, // stdin
		exec.Pipe, // stdout
		exec.Pipe) // stderr
	if err != nil {
		return execfail("Failed to run gpg.")
	}

	_, err = cmd.Stdin.WriteString(trimmedJson)
	if err != nil {
		return execfail("Failed to write to gpg.")
	}
	cmd.Stdin.Close()

	outputBytes, err := ioutil.ReadAll(cmd.Stdout)
	if err != nil {
		return execfail("Failed to read from gpg.")
	}
	output := string(outputBytes)

	errOutput, err := ioutil.ReadAll(cmd.Stderr)
	if len(errOutput) > 0 {
		log.Printf("Got error: %q", string(errOutput))
	}

	cmd.Close()

	index1 := strings.Index(output, "\n\n")
	index2 := strings.Index(output, "\n-----")
	if index1 == -1 || index2 == -1 {
		return execfail("Failed to parse signature from gpg.")
	}
	inner := output[index1+2 : index2]
	signature := strings.Replace(inner, "\n", "", -1)

	return fmt.Sprintf("%s,\"camliSig\":\"%s\"}\n", trimmedJson, signature), nil
}
