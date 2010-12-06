package main

import (
	"camli/httputil"
	"exec"
	"fmt"
	"http"
	"io/ioutil"
	"log"
	"os"
	"strings"
	"unicode"
)

func handleSign(conn http.ResponseWriter, req *http.Request) {
	if !(req.Method == "POST" && req.URL.Path == "/camli/sig/sign") {
		httputil.BadRequestError(conn, "Inconfigured handler.")
		return
	}

	req.ParseForm()

	json := req.FormValue("json")
	if json == "" {
		httputil.BadRequestError(conn, "Missing json parameter.")
		return
	}

	var keyId int
	numScanned, err := fmt.Sscanf(req.FormValue("keyid"), "%x", &keyId)
	if numScanned != 1 {
		httputil.BadRequestError(conn, "Couldn't parse keyid parameter.")
		return
	}

	trimmedJson := strings.TrimRightFunc(json, unicode.IsSpace)
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
			"--local-user", fmt.Sprintf("%x", keyId),
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
