package auth

import (
	"encoding/base64"
	"fmt"
	"http"
	"regexp"
	"strings"
)

var kBasicAuthPattern *regexp.Regexp = regexp.MustCompile(`^Basic ([a-zA-Z0-9\+/=]+)`)

var AccessPassword string

func IsAuthorized(req *http.Request) bool {
	auth, present := req.Header["Authorization"]
	if !present {
		return false
	}
	matches := kBasicAuthPattern.FindStringSubmatch(auth)
	if len(matches) != 2 {
		return false
	}
	encoded := matches[1]
	enc := base64.StdEncoding
	decBuf := make([]byte, enc.DecodedLen(len(encoded)))
	n, err := enc.Decode(decBuf, []byte(encoded))
	if err != nil {
		return false
	}
	userpass := strings.Split(string(decBuf[0:n]), ":", 2)
	if len(userpass) != 2 {
		fmt.Println("didn't get two pieces")
		return false
	}
	password := userpass[1] // username at index 0 is currently unused
	return password != "" && password == AccessPassword
}

// requireAuth wraps a function with another function that enforces
// HTTP Basic Auth.
func RequireAuth(handler func(conn http.ResponseWriter, req *http.Request)) func (conn http.ResponseWriter, req *http.Request) {
	return func (conn http.ResponseWriter, req *http.Request) {
		if !IsAuthorized(req) {
			conn.SetHeader("WWW-Authenticate", "Basic realm=\"camlistored\"")
			conn.WriteHeader(http.StatusUnauthorized)
			fmt.Fprintf(conn, "Authentication required.\n")
			return
		}
		handler(conn, req)
	}
}

