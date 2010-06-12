// Copyright 2010 Brad Fitzpatrick <brad@danga.com>
//
// See LICENSE.

package main

import "crypto/sha1"
import "flag"
import "fmt"
import "hash"
import "http"
import "io"
import "io/ioutil"
import "os"
import "regexp"

var listen *string = flag.String("listen", "0.0.0.0:3179", "host:port to listen on")
var storageRoot *string = flag.String("root", "/tmp/camliroot", "Root directory to store files")

var sharedSecret string

var kPutPattern *regexp.Regexp = regexp.MustCompile(`^/camli/(sha1)-([a-f0-9]+)$`)

func badRequestError(conn *http.Conn, errorMessage string) {
	conn.WriteHeader(http.StatusBadRequest)
        fmt.Fprintf(conn, "%s\n", errorMessage)
}

func serverError(conn *http.Conn, err os.Error) {
	conn.WriteHeader(http.StatusInternalServerError)
	fmt.Fprintf(conn, "Server error: %s\n", err)
}

func handleCamli(conn *http.Conn, req *http.Request) {
	if (req.Method == "PUT") {
		handlePut(conn, req)
		return
	}

	badRequestError(conn, "Unsupported method.")
}

func handlePut(conn *http.Conn, req *http.Request) {
	groups := kPutPattern.MatchStrings(req.URL.Path)
	if (len(groups) != 3) {
		badRequestError(conn, "Malformed PUT URL.")
		fmt.Println("PUT URL: ", req.URL.Path)
                return
	}

	hashFunc := groups[1]
	digest := groups[2]

	if (hashFunc == "sha1" && len(digest) != 40) {
		badRequestError(conn, "invalid length for sha1 hash")
		return
	}

	// TODO(bradfitz): authn/authz checks here.

	hashedDirectory := fmt.Sprintf("%s/%s/%s",
		*storageRoot,
		digest[0:3],
		digest[3:6])

	err := os.MkdirAll(hashedDirectory, 0700)
	if err != nil {
		serverError(conn, err)
		return
	}

	fileBaseName := fmt.Sprintf("%s-%s.dat", hashFunc, digest)

	tempFile, err := ioutil.TempFile(hashedDirectory, fileBaseName + ".tmp")
	if err != nil {
                serverError(conn, err)
                return
        }

	success := false  // set true later
	defer func() {
		if !success {
			fmt.Println("Removing temp file: ", tempFile.Name())
			os.Remove(tempFile.Name())
		}
	}();

	written, err := io.Copy(tempFile, req.Body)
	if err != nil {
                serverError(conn, err); return
        }
	if _, err = tempFile.Seek(0, 0); err != nil {
		serverError(conn, err); return
	}

	var hasher hash.Hash;
	switch (hashFunc) {
	case "sha1":
		hasher = sha1.New();
		break;
	}
	if (hasher == nil) {
		badRequestError(conn, "unsupported hash function.")
		return;
	}
	io.Copy(hasher, tempFile)
	if fmt.Sprintf("%x", hasher.Sum()) != digest {
		badRequestError(conn, "digest didn't match as declared.")
		return;
	}
	if err = tempFile.Close(); err != nil {
		serverError(conn, err); return
	}

	fileName := fmt.Sprintf("%s/%s", hashedDirectory, fileBaseName)
	if err = os.Rename(tempFile.Name(), fileName); err != nil {
		serverError(conn, err); return
	}

	stat, err := os.Lstat(fileName)
	if err != nil {
		serverError(conn, err); return;
	}
	if !stat.IsRegular() || stat.Size != written {
		serverError(conn, os.NewError("Written size didn't match."))
		// Unlink it?  Bogus?  Naah, better to not lose data.
		// We can clean it up later in a GC phase.
		return
	}

	success = true
	fmt.Fprint(conn, "OK")
}

func HandleRoot(conn *http.Conn, req *http.Request) {
	fmt.Fprintf(conn, `
This is camlistored, a Camlistore storage daemon.
`);
}

func main() {
	flag.Parse()

	sharedSecret = os.Getenv("CAMLI_PASSWORD")
	if len(sharedSecret) == 0 {
		fmt.Fprintf(os.Stderr,
			"No CAMLI_PASSWORD environment variable set.\n")
		os.Exit(1)
	}

	{
		fi, err := os.Stat(*storageRoot)
		if err != nil || !fi.IsDirectory() {
			fmt.Fprintf(os.Stderr,
				"Storage root '%s' doesn't exist or is not a directory.\n",
				*storageRoot)
			os.Exit(1)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", HandleRoot)
	mux.HandleFunc("/camli/", handleCamli)

	fmt.Printf("Starting to listen on http://%v/\n", *listen)
	err := http.ListenAndServe(*listen, mux)
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"Error in http server: %v\n", err)
		os.Exit(1)
	}
}
