package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var basicAuthPattern = regexp.MustCompile(`^Basic ([a-zA-Z0-9\+/=]+)`)

// basicAuth returns the username and password provided in the Authorization
// header of the request, or an error if anything went wrong.
func basicAuth(req *http.Request) (user string, password string, err error) {
	auth := req.Header.Get("Authorization")
	if auth == "" {
		return "", "", fmt.Errorf("Missing \"Authorization\" in header")
	}
	matches := basicAuthPattern.FindStringSubmatch(auth)
	if len(matches) != 2 {
		return "", "", fmt.Errorf("Bogus Authorization header")
	}
	encoded := matches[1]
	enc := base64.StdEncoding
	decBuf := make([]byte, enc.DecodedLen(len(encoded)))
	n, err := enc.Decode(decBuf, []byte(encoded))
	if err != nil {
		return "", "", err
	}
	pieces := strings.SplitN(string(decBuf[0:n]), ":", 2)
	if len(pieces) != 2 {
		return "", "", fmt.Errorf("didn't get two pieces")
	}
	return pieces[0], pieces[1], nil
}

type userPass struct {
	username string
	password string
}

func (up userPass) isAllowed(req *http.Request) bool {
	user, pass, err := basicAuth(req)
	if err != nil {
		log.Printf("Authorization failed: %v", err)
		return false
	}
	return user == up.username && pass == up.password
}

func sendUnauthorized(rw http.ResponseWriter, req *http.Request) {
	realm := ""
	rw.Header().Set("WWW-Authenticate", fmt.Sprintf("Basic realm=%q", realm))
	rw.WriteHeader(http.StatusUnauthorized)
	fmt.Fprintf(rw, "<html><body><h1>Unauthorized</h1>")
}

func newUserPass(userpass string) userPass {
	pieces := strings.Split(userpass, ":")
	if len(pieces) < 2 {
		log.Fatalf("Wrong userpass auth string: %q; needs to be \"username:password\"", userpass)
	}
	return userPass{
		username: pieces[0],
		password: pieces[1],
	}
}

func receivePost(w http.ResponseWriter, r *http.Request) {
	multipart, err := r.MultipartReader()
	if err != nil || multipart == nil {
		http.Error(w, fmt.Sprintf("multipart reader error: %v", err), http.StatusBadRequest)
		return
	}

	for {
		mimePart, err := multipart.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Printf("Error reading multipart section: %v", err)
			http.Error(w, fmt.Sprintf("Error reading multipart section: %v", err), http.StatusInternalServerError)
			return
		}

		contentDisposition, params, err := mime.ParseMediaType(mimePart.Header.Get("Content-Disposition"))
		if err != nil {
			http.Error(w, "invalid Content-Disposition", http.StatusBadRequest)
			return
		}

		if contentDisposition != "form-data" {
			http.Error(w, fmt.Sprintf("Expected Content-Disposition of \"form-data\"; got %q", contentDisposition), http.StatusBadRequest)
			return
		}

		formName := params["name"]
		if formName != "someKey" {
			http.Error(w, fmt.Sprintf("invalid form name parameter name; got %q, wanted \"someKey\"", formName), http.StatusBadRequest)
			return
		}

		var buf bytes.Buffer
		if _, err := io.Copy(&buf, mimePart); err != nil {
			log.Printf("error reading form value: %v", err)
			http.Error(w, fmt.Sprintf("error reading form value: %v", err), http.StatusInternalServerError)
			return
		}
		formValue := buf.String()
		if formValue != "someValue" {
			http.Error(w, fmt.Sprintf("invalid form value; got %q, wanted \"someValue\"", formValue), http.StatusBadRequest)
			return
		}
		fmt.Fprintf(w, "%v: Correctly received post value %q in %v to %v", time.Now(), formValue, r.Method, r.URL.Path)
		return
	}
}

// host: ":8001", "listening port and hostname"
// userpass: "foo:bar", "basic auth username and password"
func repro(host, userpass string) error {
	up := newUserPass(userpass)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			http.Error(w, "NOT A GET", http.StatusBadRequest)
			return
		}
		fmt.Fprint(w, clientCode())
	})
	mux.HandleFunc("/apiget", func(w http.ResponseWriter, r *http.Request) {
		if up.isAllowed(r) {
			if r.Method != "GET" {
				http.Error(w, "NOT A GET", http.StatusBadRequest)
				return
			}
			fmt.Fprintf(w, "Hello World, it is %v, and I received a %v for %v.", time.Now(), r.Method, r.URL.Path)
			return
		}
		sendUnauthorized(w, r)
	})
	mux.HandleFunc("/apipost", func(w http.ResponseWriter, r *http.Request) {
		if up.isAllowed(r) {
			if r.Method != "POST" {
				http.Error(w, "NOT A POST", http.StatusBadRequest)
				return
			}
			receivePost(w, r)
			return
		}
		sendUnauthorized(w, r)
	})
	t := &http.Server{
		Addr:    host,
		Handler: mux,
	}
	return t.ListenAndServe()
}

func clientCode() string {
	return `
<!doctype html>
<html>
	<head>
	</head>
	<body>
		<script>
		function reqListener () {
			console.log(this.responseText);
		}

		function get() {
			var oReq = new XMLHttpRequest();
			oReq.addEventListener("load", reqListener);
			oReq.open("GET", "/apiget");
			oReq.send();
		}
		function post() {
			var oReq = new XMLHttpRequest();
			oReq.addEventListener("load", reqListener);
			var fd = new FormData();
			fd.append("someKey", "someValue");
			oReq.open("POST", "/apipost");
			oReq.send(fd);
		}
		function postBlob() {
			var oReq = new XMLHttpRequest();
			oReq.addEventListener("load", reqListener);
			var fd = new FormData();
			fd.append("someKey", new Blob(["someValue"]));
			oReq.open("POST", "/apipost");
			oReq.send(fd);
		}
		</script>
		<p>
		Open this page in a new incognito window, otherwise some caching messes up the repro. And apparently you also need to close all the other incognito windows where you tried this, when you want to retry it in a new one.</br>
		Open the debug console.</br>
		</p>

		<p>
		Start with:</br>
		<button onclick="postBlob()">POST WITH A BLOB</button></br>
		(username: foo, password: bar)</br>
		which should send a FormData with a Blob in it,</br>
		and notice that it fails (whereas it doesn't on firefox), because the Blob in the authenticated retry was not resent with the original contents.
		</p>

		<p>
		Now do it again:</br>
		<button onclick="postBlob()">POST WITH A BLOB</button></br>
		and notice that it works this time, because the request is authenticated right from the start now, so there's no retry needed, therefore the bug can't happen.
		</p>

		<p>
		Alternatively, load the page in a new incognito window, and start with a</br>
		<button onclick="get()">GET</button> or a <button onclick="post()">POST</button> (FormData without a Blob)</br>
		and notice that not only there's no problem with any of those, but also that a subsequent</br>
		<button onclick="postBlob()">POST WITH A BLOB</button></br>
		works fine too, for the same reasons explained above (no retry needed).
		</p>

		<p>
		Note: the same kind of bug/behaviour can be observed when automatically following a 307 (which also does not fail on firefox).
		</p>
	</body>
</html>
`
}
