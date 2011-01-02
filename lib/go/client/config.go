package client

import (
	"flag"
	"log"
	"strings"
)

// These override the JSON config file ~/.camlistore's "server" and
// "password" keys
var flagServer *string = flag.String("blobserver", "", "camlistore blob server")
var flagPassword *string = flag.String("password", "", "password for blob server")

func cleanServer(server string) string {
	// Remove trailing slash if provided.
	if strings.HasSuffix(server, "/") {
		server = server[0 : len(server)-1]
	}
	// Add "http://" prefix if not present:
	if !strings.HasPrefix(server, "http") {
		server = "http://" + server
	}
	return server
}

func blobServerOrDie() string {
	if *flagServer != "" {
		return cleanServer(*flagServer)
	}
	log.Exitf("No --blobserver parameter specified.")
	return ""
}

func passwordOrDie() string {
	if *flagPassword != "" {
		return *flagPassword
	}
	log.Exitf("No --password parameter specified.")
	return ""
}

