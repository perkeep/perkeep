package main

import (
	"flag"
	"fmt"
	"http"
	"os"
)

var flagRing *string = flag.String("keyring", "./test/test-keyring.gpg",
	"GnuPG public keyring file to use.")

var flagSecretRing *string = flag.String("secret-keyring", "./test/test-secring.gpg",
	"GnuPG secret keyring file to use.")

var listen *string = flag.String("listen", "0.0.0.0:2856", "host:port to listen on")

var accessPassword string

func main() {
	flag.Parse()

	accessPassword = os.Getenv("CAMLI_PASSWORD")
	if len(accessPassword) == 0 {
		fmt.Fprintf(os.Stderr,
			"No CAMLI_PASSWORD environment variable set.\n")
		os.Exit(1)
	}

	mux := http.NewServeMux()
	fmt.Printf("Starting to listen on http://%v/\n", *listen)
	err := http.ListenAndServe(*listen, mux)
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"Error in http server: %v\n", err)
		os.Exit(1)
	}
}
