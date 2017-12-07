package mailgun

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/facebookgo/ensure"
)

func TestGetBounces(t *testing.T) {
	mg, err := NewMailgunFromEnv()
	ensure.Nil(t, err)

	n, bounces, err := mg.GetBounces(-1, -1)
	ensure.Nil(t, err)
	ensure.DeepEqual(t, n, len(bounces))
}

func TestGetSingleBounce(t *testing.T) {
	domain := reqEnv(t, "MG_DOMAIN")
	mg, err := NewMailgunFromEnv()
	ensure.Nil(t, err)

	exampleEmail := fmt.Sprintf("%s@%s", strings.ToLower(randomString(64, "")), domain)
	_, err = mg.GetSingleBounce(exampleEmail)
	ensure.NotNil(t, err)

	ure, ok := err.(*UnexpectedResponseError)
	ensure.True(t, ok)
	ensure.DeepEqual(t, ure.Actual, http.StatusNotFound)
}

func TestAddDelBounces(t *testing.T) {
	domain := reqEnv(t, "MG_DOMAIN")
	mg, err := NewMailgunFromEnv()
	ensure.Nil(t, err)

	// Compute an e-mail address for our domain.
	exampleEmail := fmt.Sprintf("%s@%s", strings.ToLower(randomString(8, "bounce")), domain)

	// First, basic sanity check.
	// Fail early if we have bounces for a fictitious e-mail address.

	n, _, err := mg.GetBounces(-1, -1)
	ensure.Nil(t, err)
	// Add the bounce for our address.

	err = mg.AddBounce(exampleEmail, "550", "TestAddDelBounces-generated error")
	ensure.Nil(t, err)

	// We should now have one bounce listed when we query the API.

	n, bounces, err := mg.GetBounces(-1, -1)
	ensure.Nil(t, err)
	if n == 0 {
		t.Fatal("Expected at least one bounce for this domain.")
	}

	found := 0
	for _, bounce := range bounces {
		t.Logf("Bounce Address: %s\n", bounce.Address)
		if bounce.Address == exampleEmail {
			found++
		}
	}

	if found == 0 {
		t.Fatalf("Expected bounce for address %s in list of bounces", exampleEmail)
	}

	bounce, err := mg.GetSingleBounce(exampleEmail)
	ensure.Nil(t, err)
	if bounce.CreatedAt == "" {
		t.Fatalf("Expected at least one bounce for %s", exampleEmail)
	}

	// Delete it.  This should put us back the way we were.

	err = mg.DeleteBounce(exampleEmail)
	ensure.Nil(t, err)

	// Make sure we're back to the way we were.

	n, bounces, err = mg.GetBounces(-1, -1)
	ensure.Nil(t, err)

	found = 0
	for _, bounce := range bounces {
		t.Logf("Bounce Address: %s\n", bounce.Address)
		if bounce.Address == exampleEmail {
			found++
		}
	}

	if found != 0 {
		t.Fatalf("Expected no bounce for address %s in list of bounces", exampleEmail)
	}

	_, err = mg.GetSingleBounce(exampleEmail)
	ensure.NotNil(t, err)
}
