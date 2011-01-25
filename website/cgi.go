package main

// Package cgi implements a CGI (Common Gateway Interface) HTTP handler as specified
// in RFC 3875.  Using CGI isn't usually incredibly efficient and this package
// is intended primarily for compatibility.

import (
	"camli/encoding/line"
	"exec"
	"fmt"
	"http"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
)

type CgiHandler struct {
	ExecutablePath string
	// TODO: custom log.Logger
}

func (h *CgiHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	env := []string{
		"SERVER_SOFTWARE=golang",
		"SERVER_NAME=" + req.Host,
		"GATEWAY_INTERFACE=CGI/1.1",
		"REQUEST_METHOD=" + req.Method,
 		"QUERY_STRING=" + req.URL.RawQuery,
	}

	if req.ContentLength > 0 {
		env = append(env, fmt.Sprintf("CONTENT_LENGTH=%d", req.ContentLength))
	}
	if ctype, ok := req.Header["Content-Type"]; ok {
		env = append(env, "CONTENT_TYPE=" + ctype)
	}

	// TODO: let this be cleared instead at start.
	env = append(env, os.Environ()...)

	cwd := h.ExecutablePath
	if slash := strings.LastIndex(cwd, "/"); slash != -1 {
		cwd = cwd[0:slash]
	}
	if !strings.HasPrefix(cwd, "/") {
		cwd = "."
	}

	cmd, err := exec.Run(
		h.ExecutablePath,
		[]string{h.ExecutablePath},
		env,
		cwd,
		exec.Pipe,  // stdin
		exec.Pipe,  // stdout
		exec.PassThrough,  // stderr (for now)
		)
	if err != nil {
		rw.WriteHeader(http.StatusInternalServerError)
		log.Printf("CGI error: %v", err)
		return
	}
	defer func() {
		// TODO: close subprocess somehow? No kill?!
		cmd.Stdin.Close()
		cmd.Stdout.Close()
	}()

	if req.ContentLength != 0 {
		go func() {
			io.Copy(cmd.Stdin, req.Body)
		}()
	}

	linebody := line.NewReader(cmd.Stdout, 1024)
	headers := make(map[string]string)
	statusCode := http.StatusOK
	for {
		line, isPrefix, err := linebody.ReadLine()
		if isPrefix {
			rw.WriteHeader(http.StatusInternalServerError)
			log.Printf("CGI: long header line from subprocess.")
			return
		}
		if err == os.EOF {
			break
		}
		if err != nil {
			rw.WriteHeader(http.StatusInternalServerError)
			log.Printf("CGI: error reading headers: %v", err)
			return
		}
		if len(line) == 0 {
			break
		}
		parts := strings.Split(string(line), ":", 2)
		if len(parts) < 2 {
			log.Printf("CGI: bogus header line: %s", string(line))
			continue
		}
		h, v := parts[0], parts[1]
		h = strings.TrimSpace(h)
		v = strings.TrimSpace(v)
		switch {
		case h == "Status":
			if len(v) < 3 {
				log.Printf("CGI: bogus status (short): %q", v)
				return
			}
			code, err := strconv.Atoi(v[0:3])
			if err != nil {
				log.Printf("CGI: bogus status: %q", v)
				log.Printf("CGI: line was %q", line)
				return
			}
			statusCode = code
		default:
			headers[h] = v
		}
	}
	for h, v := range headers {
		rw.SetHeader(h, v)
	}
	rw.WriteHeader(statusCode)

	_, err = io.Copy(rw, linebody)
	if err != nil {
		log.Printf("CGI: copy error: %v", err)
	}
}
