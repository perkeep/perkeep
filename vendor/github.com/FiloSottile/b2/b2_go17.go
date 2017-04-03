// +build go1.7

package b2

import (
	"net/http"
	"net/http/httptrace"
)

func addTracing(req *http.Request) *http.Request {
	trace := &httptrace.ClientTrace{
		ConnectStart: func(network, addr string) {
			debugf("new connection to %s (for %s)", addr, req.URL.Path)
		},
	}
	return req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
}

func init() {
	requestExtFunc = addTracing
}
