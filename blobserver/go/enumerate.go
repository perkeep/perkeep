package main

import (
	"fmt"
	"http"
)

func handleEnumerateBlobs(conn *http.Conn, req *http.Request) {
	fmt.Fprintf(conn, "Unsupported.");
}

