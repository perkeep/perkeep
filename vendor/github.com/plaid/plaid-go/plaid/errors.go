package plaid

import (
	"fmt"
)

type plaidError struct {
	// List of all errors: https://github.com/plaid/support/blob/master/errors.md
	ErrorCode int    `json:"code"`
	Message   string `json:"message"`
	Resolve   string `json:"resolve"`

	// StatusCode needs to manually set from the http response
	StatusCode int
}

func (e plaidError) Error() string {
	return fmt.Sprintf("Plaid Error - http status: %d, code: %d, message: %s, resolve: %s",
		e.StatusCode, e.ErrorCode, e.Message, e.Resolve)
}
