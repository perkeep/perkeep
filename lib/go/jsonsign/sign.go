package jsonsign

import (
	"camli/blobref"
	"exec"
	"flag"
	"fmt"
	"io/ioutil"
	"json"
	"log"
	"os"
	"strings"
	"unicode"
)

var gpgPath *string = flag.String("gpg-path", "/usr/bin/gpg", "Path to the gpg binary.")

var flagRing *string = flag.String("keyring", "./test/test-keyring.gpg",
	"GnuPG public keyring file to use.")

var flagSecretRing *string = flag.String("secret-keyring", "./test/test-secring.gpg",
	"GnuPG secret keyring file to use.")


type SignRequest struct {
	UnsignedJson  string
	Fetcher       blobref.Fetcher
	UseAgent      bool

	// In server-mode, don't use any default (user) keys
	// TODO: formalize what this means?
	ServerMode    bool
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

	signerBlob := blobref.Parse(camliSigner.(string))
	if signerBlob == nil {
		return inputfail("json \"camliSigner\" key is malformed or unsupported")
	}

	pubkeyReader, _, err := sr.Fetcher.Fetch(signerBlob)
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
	trimmedJson = trimmedJson[0:len(trimmedJson)-1]

	args := []string{"gpg",
			"--local-user", pk.KeyIdString(),
			"--detach-sign",
			"--armor"}

	if sr.UseAgent {
		args = append(args, "--use-agent")
	}

	if sr.ServerMode {
		args = append(args,
			"--no-default-keyring",
			"--keyring", *flagRing, // TODO: needed for signing?
			"--secret-keyring", *flagSecretRing)
	}

	args = append(args, "-")

	cmd, err := exec.Run(
		*gpgPath,
		args,
		os.Environ(),
		".",
		exec.Pipe,  // stdin
		exec.Pipe,  // stdout
		exec.Pipe)  // stderr
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
	if (index1 == -1 || index2 == -1) {
		return execfail("Failed to parse signature from gpg.")
	}
	inner := output[index1+2:index2]
	signature := strings.Replace(inner, "\n", "", -1)

	return fmt.Sprintf("%s,\"camliSig\":\"%s\"}\n", trimmedJson, signature), nil
}

