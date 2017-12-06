package mailgun

import (
	"testing"

	"github.com/facebookgo/ensure"
)

func TestEmailValidation(t *testing.T) {
	reqEnv(t, "MG_PUBLIC_API_KEY")
	mg, err := NewMailgunFromEnv()
	ensure.Nil(t, err)

	ev, err := mg.ValidateEmail("foo@mailgun.com")
	ensure.Nil(t, err)

	ensure.True(t, ev.IsValid)
	ensure.False(t, ev.IsDisposableAddress)
	ensure.False(t, ev.IsRoleAddress)
	ensure.True(t, ev.Parts.DisplayName == "")
	ensure.DeepEqual(t, ev.Parts.LocalPart, "foo")
	ensure.DeepEqual(t, ev.Parts.Domain, "mailgun.com")
}

func TestParseAddresses(t *testing.T) {
	reqEnv(t, "MG_PUBLIC_API_KEY")
	mg, err := NewMailgunFromEnv()
	ensure.Nil(t, err)

	addressesThatParsed, unparsableAddresses, err := mg.ParseAddresses(
		"Alice <alice@example.com>",
		"bob@example.com",
		"example.com")
	ensure.Nil(t, err)
	hittest := map[string]bool{
		"Alice <alice@example.com>": true,
		"bob@example.com":           true,
	}
	for _, a := range addressesThatParsed {
		ensure.True(t, hittest[a])
	}
	ensure.True(t, len(unparsableAddresses) == 1)
}
