package mailgun

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/facebookgo/ensure"
)

func TestWebhookCRUD(t *testing.T) {
	mg, err := NewMailgunFromEnv()
	ensure.Nil(t, err)

	var countHooks = func() int {
		hooks, err := mg.GetWebhooks()
		ensure.Nil(t, err)
		return len(hooks)
	}

	hookCount := countHooks()

	domainURL := randomDomainURL(10)
	ensure.Nil(t, mg.CreateWebhook("deliver", domainURL))
	defer func() {
		ensure.Nil(t, mg.DeleteWebhook("deliver"))
		newCount := countHooks()
		ensure.DeepEqual(t, newCount, hookCount)
	}()

	newCount := countHooks()
	ensure.False(t, newCount <= hookCount)

	theURL, err := mg.GetWebhookByType("deliver")
	ensure.Nil(t, err)
	ensure.DeepEqual(t, theURL, domainURL)

	updatedDomainURL := randomDomainURL(10)
	ensure.Nil(t, mg.UpdateWebhook("deliver", updatedDomainURL))

	hooks, err := mg.GetWebhooks()
	ensure.Nil(t, err)

	ensure.DeepEqual(t, hooks["deliver"], updatedDomainURL)
}

var signedTests = []bool{
	true,
	false,
}

func TestVerifyWebhookRequest_Form(t *testing.T) {
	mg, err := NewMailgunFromEnv()
	ensure.Nil(t, err)

	for _, v := range signedTests {
		fields := getSignatureFields(mg.ApiKey(), v)
		req := buildFormRequest(fields)

		verified, err := mg.VerifyWebhookRequest(req)
		ensure.Nil(t, err)

		if v != verified {
			t.Errorf("VerifyWebhookRequest should return '%v' but got '%v'", v, verified)
		}
	}
}

func TestVerifyWebhookRequest_MultipartForm(t *testing.T) {
	mg, err := NewMailgunFromEnv()
	ensure.Nil(t, err)

	for _, v := range signedTests {
		fields := getSignatureFields(mg.ApiKey(), v)
		req := buildMultipartFormRequest(fields)

		verified, err := mg.VerifyWebhookRequest(req)
		ensure.Nil(t, err)

		if v != verified {
			t.Errorf("VerifyWebhookRequest should return '%v' but got '%v'", v, verified)
		}
	}
}

func buildFormRequest(fields map[string]string) *http.Request {
	values := url.Values{}

	for k, v := range fields {
		values.Add(k, v)
	}

	r := strings.NewReader(values.Encode())
	req, _ := http.NewRequest("POST", "/", r)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	return req
}

func buildMultipartFormRequest(fields map[string]string) *http.Request {
	buf := &bytes.Buffer{}
	writer := multipart.NewWriter(buf)

	for k, v := range fields {
		writer.WriteField(k, v)
	}

	writer.Close()

	req, _ := http.NewRequest("POST", "/", buf)
	req.Header.Set("Content-type", writer.FormDataContentType())

	return req
}

func getSignatureFields(key string, signed bool) map[string]string {
	badSignature := hex.EncodeToString([]byte("badsignature"))

	fields := map[string]string{
		"token":     "token",
		"timestamp": "123456789",
		"signature": badSignature,
	}

	if signed {
		h := hmac.New(sha256.New, []byte(key))
		io.WriteString(h, fields["timestamp"])
		io.WriteString(h, fields["token"])
		hash := h.Sum(nil)

		fields["signature"] = hex.EncodeToString(hash)
	}

	return fields
}
