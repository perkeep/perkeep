// +build go1.7

package oauth

import (
	"context"
	"net/http"
)

func requestWithContext(ctx context.Context, req *http.Request) *http.Request {
	return req.WithContext(ctx)
}
