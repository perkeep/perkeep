package mailgun

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/facebookgo/ensure"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

const domain = "valid-mailgun-domain"
const apiKey = "valid-mailgun-api-key"
const publicApiKey = "valid-mailgun-public-api-key"

func TestMailgunGinkgo(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Mailgun Test Suite")
}

func TestMailgun(t *testing.T) {
	m := NewMailgun(domain, apiKey, publicApiKey)

	ensure.DeepEqual(t, m.Domain(), domain)
	ensure.DeepEqual(t, m.ApiKey(), apiKey)
	ensure.DeepEqual(t, m.PublicApiKey(), publicApiKey)
	ensure.DeepEqual(t, m.Client(), http.DefaultClient)

	client := new(http.Client)
	m.SetClient(client)
	ensure.DeepEqual(t, client, m.Client())
}

func TestBounceGetCode(t *testing.T) {
	b1 := &Bounce{
		CreatedAt: "blah",
		Code:      123,
		Address:   "blort",
		Error:     "bletch",
	}
	c, err := b1.GetCode()
	ensure.Nil(t, err)
	ensure.DeepEqual(t, c, 123)

	b2 := &Bounce{
		CreatedAt: "blah",
		Code:      "456",
		Address:   "blort",
		Error:     "Bletch",
	}
	c, err = b2.GetCode()
	ensure.Nil(t, err)
	ensure.DeepEqual(t, c, 456)

	b3 := &Bounce{
		CreatedAt: "blah",
		Code:      "456H",
		Address:   "blort",
		Error:     "Bletch",
	}
	c, err = b3.GetCode()
	ensure.NotNil(t, err)

	e, ok := err.(*strconv.NumError)
	if !ok && e != nil {
		t.Fatal("Expected a syntax error in numeric conversion: got ", err)
	}
}
