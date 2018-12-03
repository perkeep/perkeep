// +build !go1.7

package oauth

import (
	"net/http"

	"golang.org/x/net/context"
)

func requestWithContext(ctx context.Context, req *http.Request) *http.Request {
	return req
}
