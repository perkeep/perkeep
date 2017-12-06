package mailgun

import (
	"net/http"
	"testing"

	"github.com/facebookgo/ensure"
)

func TestGetDomains(t *testing.T) {
	mg, err := NewMailgunFromEnv()
	ensure.Nil(t, err)

	n, domains, err := mg.GetDomains(DefaultLimit, DefaultSkip)
	ensure.Nil(t, err)

	t.Logf("TestGetDomains: %d domains retrieved\n", n)
	for _, d := range domains {
		t.Logf("TestGetDomains: %#v\n", d)
	}
}

func TestGetSingleDomain(t *testing.T) {
	mg, err := NewMailgunFromEnv()
	ensure.Nil(t, err)

	_, domains, err := mg.GetDomains(DefaultLimit, DefaultSkip)
	ensure.Nil(t, err)

	dr, rxDnsRecords, txDnsRecords, err := mg.GetSingleDomain(domains[0].Name)
	ensure.Nil(t, err)

	t.Logf("TestGetSingleDomain: %#v\n", dr)
	for _, rxd := range rxDnsRecords {
		t.Logf("TestGetSingleDomains:   %#v\n", rxd)
	}
	for _, txd := range txDnsRecords {
		t.Logf("TestGetSingleDomains:   %#v\n", txd)
	}
}

func TestGetSingleDomainNotExist(t *testing.T) {
	mg, err := NewMailgunFromEnv()
	ensure.Nil(t, err)
	_, _, _, err = mg.GetSingleDomain(randomString(32, "com.edu.org.") + ".com")
	if err == nil {
		t.Fatal("Did not expect a domain to exist")
	}
	ure, ok := err.(*UnexpectedResponseError)
	ensure.True(t, ok)
	ensure.DeepEqual(t, ure.Actual, http.StatusNotFound)
}

func TestAddDeleteDomain(t *testing.T) {
	mg, err := NewMailgunFromEnv()
	ensure.Nil(t, err)

	// First, we need to add the domain.
	randomDomainName := randomString(16, "DOMAIN") + ".example.com"
	randomPassword := randomString(16, "PASSWD")
	ensure.Nil(t, mg.CreateDomain(randomDomainName, randomPassword, Tag, false))
	// Next, we delete it.
	ensure.Nil(t, mg.DeleteDomain(randomDomainName))
}
