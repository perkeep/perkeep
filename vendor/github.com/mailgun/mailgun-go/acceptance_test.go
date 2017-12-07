package mailgun

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"io"
	"io/ioutil"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/facebookgo/ensure"
	"github.com/onsi/ginkgo"
)

// Many tests require configuration settings unique to the user, passed in via
// environment variables.  If these variables aren't set, we need to fail the test early.
func reqEnv(t ginkgo.GinkgoTInterface, variableName string) string {
	value := os.Getenv(variableName)
	ensure.True(t, value != "")
	return value
}

func randomDomainURL(n int) string {
	const alpha = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	var bytes = make([]byte, n)
	rand.Read(bytes)
	for i, b := range bytes {
		bytes[i] = alpha[b%byte(len(alpha))]
	}
	return "http://" + string(bytes) + ".com"
}

// randomString generates a string of given length, but random content.
// All content will be within the ASCII graphic character set.
// (Implementation from Even Shaw's contribution on
// http://stackoverflow.com/questions/12771930/what-is-the-fastest-way-to-generate-a-long-random-string-in-go).
func randomString(n int, prefix string) string {
	const alphanum = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	var bytes = make([]byte, n)
	rand.Read(bytes)
	for i, b := range bytes {
		bytes[i] = alphanum[b%byte(len(alphanum))]
	}
	return prefix + string(bytes)
}

func randomEmail(prefix, domain string) string {
	return strings.ToLower(fmt.Sprintf("%s@%s", randomString(20, prefix), domain))
}

func spendMoney(t *testing.T, tFunc func()) {
	ok := os.Getenv("MG_SPEND_MONEY")
	if ok != "" {
		tFunc()
	} else {
		t.Log("Money spending not allowed, not running function.")
	}
}

func parseContentType(req *http.Request) (url.Values, error) {
	contentType, attrs, _ := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if contentType != "multipart/form-data" {
		return nil, fmt.Errorf("unexpected content type: %v", contentType)
	}
	raw, err := ioutil.ReadAll(req.Body)
	if err != nil {
		return nil, fmt.Errorf("err reading body of multipart request snapshot: %v", err)
	}
	boundary := attrs["boundary"]
	reader := multipart.NewReader(bytes.NewReader(raw), boundary)
	values := url.Values{}
	for {
		part, err := reader.NextPart()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("err getting nextpart: %v", err)
		}
		data, err := ioutil.ReadAll(part)
		if err != nil {
			return nil, fmt.Errorf("err getting data from part: %v", err)
		}
		if bytes.Contains(data, []byte(boundary)) {
			return nil, fmt.Errorf("part contains the boundary, which is not expected")
		}
		values.Set(part.FormName(), string(data))
		if err = part.Close(); err != nil {
			return nil, fmt.Errorf("err closing part: %v", err)
		}
	}
	return values, nil
}
