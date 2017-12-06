package mailgun

import (
	"strconv"
	"time"
)

// Bounce aggregates data relating to undeliverable messages to a specific intended recipient,
// identified by Address.
// Code provides the SMTP error code causing the bounce,
// while Error provides a human readable reason why.
// CreatedAt provides the time at which Mailgun detected the bounce.
type Bounce struct {
	CreatedAt string      `json:"created_at"`
	Code      interface{} `json:"code"`
	Address   string      `json:"address"`
	Error     string      `json:"error"`
}

type Paging struct {
	First    string `json:"first,omitempty"`
	Next     string `json:"next,omitempty"`
	Previous string `json:"previous,omitempty"`
	Last     string `json:"last,omitempty"`
}

type bounceEnvelope struct {
	Items  []Bounce `json:"items"`
	Paging Paging   `json:"paging"`
}

// GetCreatedAt parses the textual, RFC-822 timestamp into a standard Go-compatible
// Time structure.
func (i Bounce) GetCreatedAt() (t time.Time, err error) {
	return parseMailgunTime(i.CreatedAt)
}

// GetCode will return the bounce code for the message, regardless if it was
// returned as a string or as an integer.  This method overcomes a protocol
// bug in the Mailgun API.
func (b Bounce) GetCode() (int, error) {
	switch c := b.Code.(type) {
	case int:
		return c, nil
	case string:
		return strconv.Atoi(c)
	default:
		return -1, strconv.ErrSyntax
	}
}

// GetBounces returns a complete set of bounces logged against the sender's domain, if any.
// The results include the total number of bounces (regardless of skip or limit settings),
// and the slice of bounces specified, if successful.
// Note that the length of the slice may be smaller than the total number of bounces.
func (m *MailgunImpl) GetBounces(limit, skip int) (int, []Bounce, error) {
	r := newHTTPRequest(generateApiUrl(m, bouncesEndpoint))
	if limit != -1 {
		r.addParameter("limit", strconv.Itoa(limit))
	}
	if skip != -1 {
		r.addParameter("skip", strconv.Itoa(skip))
	}

	r.setClient(m.Client())
	r.setBasicAuth(basicAuthUser, m.ApiKey())

	var response bounceEnvelope
	err := getResponseFromJSON(r, &response)
	if err != nil {
		return -1, nil, err
	}

	return len(response.Items), response.Items, nil
}

// GetSingleBounce retrieves a single bounce record, if any exist, for the given recipient address.
func (m *MailgunImpl) GetSingleBounce(address string) (Bounce, error) {
	r := newHTTPRequest(generateApiUrl(m, bouncesEndpoint) + "/" + address)
	r.setClient(m.Client())
	r.setBasicAuth(basicAuthUser, m.ApiKey())

	var response Bounce
	err := getResponseFromJSON(r, &response)
	return response, err
}

// AddBounce files a bounce report.
// Address identifies the intended recipient of the message that bounced.
// Code corresponds to the numeric response given by the e-mail server which rejected the message.
// Error providees the corresponding human readable reason for the problem.
// For example,
// here's how the these two fields relate.
// Suppose the SMTP server responds with an error, as below.
// Then, . . .
//
//      550  Requested action not taken: mailbox unavailable
//     \___/\_______________________________________________/
//       |                         |
//       `-- Code                  `-- Error
//
// Note that both code and error exist as strings, even though
// code will report as a number.
func (m *MailgunImpl) AddBounce(address, code, error string) error {
	r := newHTTPRequest(generateApiUrl(m, bouncesEndpoint))
	r.setClient(m.Client())
	r.setBasicAuth(basicAuthUser, m.ApiKey())

	payload := newUrlEncodedPayload()
	payload.addValue("address", address)
	if code != "" {
		payload.addValue("code", code)
	}
	if error != "" {
		payload.addValue("error", error)
	}
	_, err := makePostRequest(r, payload)
	return err
}

// DeleteBounce removes all bounces associted with the provided e-mail address.
func (m *MailgunImpl) DeleteBounce(address string) error {
	r := newHTTPRequest(generateApiUrl(m, bouncesEndpoint) + "/" + address)
	r.setClient(m.Client())
	r.setBasicAuth(basicAuthUser, m.ApiKey())
	_, err := makeDeleteRequest(r)
	return err
}
