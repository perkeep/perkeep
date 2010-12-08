package main

import (
	"camli/blobref"
	"camli/httputil"
	"exec"
	"fmt"
	"http"
	"io/ioutil"
	"json"
	"log"
	"os"
	"strings"
	"unicode"
)

const kMaxJsonLength = 1024 * 1024

func handleSign(conn http.ResponseWriter, req *http.Request) {
	if !(req.Method == "POST" && req.URL.Path == "/camli/sig/sign") {
		httputil.BadRequestError(conn, "Inconfigured handler.")
		return
	}

	req.ParseForm()

	jsonStr := req.FormValue("json")
	if jsonStr == "" {
		httputil.BadRequestError(conn, "Missing json parameter")
		return
	}
	if len(jsonStr) > kMaxJsonLength {
		httputil.BadRequestError(conn, "json parameter too large")
		return
	}

	trimmedJson := strings.TrimRightFunc(jsonStr, unicode.IsSpace)

	jmap := make(map[string]interface{})
	if err := json.Unmarshal([]byte(trimmedJson), &jmap); err != nil {
		httputil.BadRequestError(conn, "json parameter doesn't parse as JSON.")
                return
	}

	camliSigner, hasSigner := jmap["camliSigner"]
	if !hasSigner {
		httputil.BadRequestError(conn, "json lacks \"camliSigner\" key with public key blobref")
                return
	}

	signerBlob := blobref.Parse(camliSigner.(string))
	if signerBlob == nil {
		httputil.BadRequestError(conn, "json \"camliSigner\" key is malformed or unsupported")
                return
	}

	publicKeyFile := fmt.Sprintf("%s/%s.camli", *flagPubKeyDir, signerBlob.String())
	pk, err := openArmoredPublicKeyFile(publicKeyFile)
	if err != nil {
		conn.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintf(conn, "Failed to find public key for %s", signerBlob)
		return
		}

	// This check should be redundant if the above JSON parse succeeded, but
	// for explicitness...
	if len(trimmedJson) == 0 || trimmedJson[len(trimmedJson)-1] != '}' {
		httputil.BadRequestError(conn, "json parameter lacks trailing '}'.")
		return
	}
	trimmedJson = trimmedJson[0:len(trimmedJson)-1]

	cmd, err := exec.Run(
		*gpgPath,
		[]string{
			"--no-default-keyring",
			"--keyring", *flagRing,
			"--secret-keyring", *flagSecretRing,
			"--local-user", pk.KeyIdString(),
			"--detach-sign",
			"--armor",
			"-"},
		os.Environ(),
		".",
		exec.Pipe,  // stdin
		exec.Pipe,  // stdout
		exec.Pipe)  // stderr
	if err != nil {
		httputil.BadRequestError(conn, "Failed to run gpg.")
		return
	}

	_, err = cmd.Stdin.WriteString(trimmedJson)
	if err != nil {
		httputil.BadRequestError(conn, "Failed to write to gpg.")
		return
	}
	cmd.Stdin.Close()

	outputBytes, err := ioutil.ReadAll(cmd.Stdout)
	if err != nil {
		httputil.BadRequestError(conn, "Failed to read from gpg.")
		return
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
		httputil.BadRequestError(conn, "Failed to parse signature from gpg.")
		return
	}
	inner := output[index1+2:index2]
	signature := strings.Replace(inner, "\n", "", -1)

	signedJson := fmt.Sprintf("%s,\"camliSig\":\"%s\"}\n", trimmedJson, signature)
	conn.Write([]byte(signedJson))
}
