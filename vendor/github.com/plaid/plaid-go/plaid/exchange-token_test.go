package plaid

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestExchangeToken(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "exchange-token tests")
}

var _ = Describe("exchange-token", func() {
	Describe("ExchangeToken", func() {
		It("returns public_token and access_token", func() {
			c := NewClient("test_id", "test_secret", Tartan)
			res, err := c.ExchangeToken("test,chase,connected")
			Expect(err).To(BeNil(), "err should be nil")
			Expect(res.AccessToken).To(Equal("test_chase"))
		})
	})
})
