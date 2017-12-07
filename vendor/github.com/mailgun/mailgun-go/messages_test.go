package mailgun

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/facebookgo/ensure"
)

const (
	fromUser       = "=?utf-8?q?Katie_Brewer=2C_CFP=C2=AE?= <joe@example.com>"
	exampleSubject = "Mailgun-go Example Subject"
	exampleText    = "Testing some Mailgun awesomeness!"
	exampleHtml    = "<html><head /><body><p>Testing some <a href=\"http://google.com?q=abc&r=def&s=ghi\">Mailgun HTML awesomeness!</a> at www.kc5tja@yahoo.com</p></body></html>"

	exampleMime = `Content-Type: text/plain; charset="ascii"
Subject: Joe's Example Subject
From: Joe Example <joe@example.com>
To: BARGLEGARF <sam.falvo@rackspace.com>
Content-Transfer-Encoding: 7bit
Date: Thu, 6 Mar 2014 00:37:52 +0000

Testing some Mailgun MIME awesomeness!
`
	templateText = "Greetings %recipient.name%!  Your reserved seat is at table %recipient.table%."
)

func TestSendLegacyPlain(t *testing.T) {
	spendMoney(t, func() {
		toUser := reqEnv(t, "MG_EMAIL_TO")
		mg, err := NewMailgunFromEnv()
		ensure.Nil(t, err)

		m := NewMessage(fromUser, exampleSubject, exampleText, toUser)
		msg, id, err := mg.Send(m)
		ensure.Nil(t, err)
		t.Log("TestSendPlain:MSG(" + msg + "),ID(" + id + ")")
	})
}

func TestSendLegacyPlainWithTracking(t *testing.T) {
	spendMoney(t, func() {
		toUser := reqEnv(t, "MG_EMAIL_TO")
		mg, err := NewMailgunFromEnv()
		ensure.Nil(t, err)

		m := NewMessage(fromUser, exampleSubject, exampleText, toUser)
		m.SetTracking(true)
		msg, id, err := mg.Send(m)
		ensure.Nil(t, err)
		t.Log("TestSendPlainWithTracking:MSG(" + msg + "),ID(" + id + ")")
	})
}

func TestSendLegacyPlainAt(t *testing.T) {
	spendMoney(t, func() {
		toUser := reqEnv(t, "MG_EMAIL_TO")
		mg, err := NewMailgunFromEnv()
		ensure.Nil(t, err)

		m := NewMessage(fromUser, exampleSubject, exampleText, toUser)
		m.SetDeliveryTime(time.Now().Add(5 * time.Minute))
		msg, id, err := mg.Send(m)
		ensure.Nil(t, err)
		t.Log("TestSendPlainAt:MSG(" + msg + "),ID(" + id + ")")
	})
}

func TestSendLegacyHtml(t *testing.T) {
	spendMoney(t, func() {
		toUser := reqEnv(t, "MG_EMAIL_TO")
		mg, err := NewMailgunFromEnv()
		ensure.Nil(t, err)

		m := NewMessage(fromUser, exampleSubject, exampleText, toUser)
		m.SetHtml(exampleHtml)
		msg, id, err := mg.Send(m)
		ensure.Nil(t, err)
		t.Log("TestSendHtml:MSG(" + msg + "),ID(" + id + ")")
	})
}

func TestSendLegacyTracking(t *testing.T) {
	spendMoney(t, func() {
		toUser := reqEnv(t, "MG_EMAIL_TO")
		mg, err := NewMailgunFromEnv()
		ensure.Nil(t, err)

		m := NewMessage(fromUser, exampleSubject, exampleText+"Tracking!\n", toUser)
		m.SetTracking(false)
		msg, id, err := mg.Send(m)
		ensure.Nil(t, err)
		t.Log("TestSendTracking:MSG(" + msg + "),ID(" + id + ")")
	})
}

func TestSendLegacyTag(t *testing.T) {
	spendMoney(t, func() {
		toUser := reqEnv(t, "MG_EMAIL_TO")
		mg, err := NewMailgunFromEnv()
		ensure.Nil(t, err)

		m := NewMessage(fromUser, exampleSubject, exampleText+"Tags Galore!\n", toUser)
		m.AddTag("FooTag")
		m.AddTag("BarTag")
		m.AddTag("BlortTag")
		msg, id, err := mg.Send(m)
		ensure.Nil(t, err)
		t.Log("TestSendTag:MSG(" + msg + "),ID(" + id + ")")
	})
}

func TestSendLegacyMIME(t *testing.T) {
	spendMoney(t, func() {
		toUser := reqEnv(t, "MG_EMAIL_TO")
		mg, err := NewMailgunFromEnv()
		ensure.Nil(t, err)

		m := NewMIMEMessage(ioutil.NopCloser(strings.NewReader(exampleMime)), toUser)
		msg, id, err := mg.Send(m)
		ensure.Nil(t, err)
		t.Log("TestSendMIME:MSG(" + msg + "),ID(" + id + ")")
	})
}

func TestGetStoredMessage(t *testing.T) {
	spendMoney(t, func() {
		mg, err := NewMailgunFromEnv()
		ensure.Nil(t, err)

		id, err := findStoredMessageID(mg) // somehow...
		if err != nil {
			t.Log(err)
			return
		}

		// First, get our stored message.
		msg, err := mg.GetStoredMessage(id)
		ensure.Nil(t, err)

		fields := map[string]string{
			"       From": msg.From,
			"     Sender": msg.Sender,
			"    Subject": msg.Subject,
			"Attachments": fmt.Sprintf("%d", len(msg.Attachments)),
			"    Headers": fmt.Sprintf("%d", len(msg.MessageHeaders)),
		}
		for k, v := range fields {
			fmt.Printf("%13s: %s\n", k, v)
		}

		// We're done with it; now delete it.
		ensure.Nil(t, mg.DeleteStoredMessage(id))
	})
}

// Tries to locate the first stored event type, returning the associated stored message key.
func findStoredMessageID(mg Mailgun) (string, error) {
	ei := mg.NewEventIterator()
	err := ei.GetFirstPage(GetEventsOptions{})
	for {
		if err != nil {
			return "", err
		}
		if len(ei.Events) == 0 {
			break
		}
		for _, event := range ei.Events {
			if event.Event == EventStored {
				return event.Storage.Key, nil
			}
		}
		err = ei.GetNext()
	}
	return "", fmt.Errorf("No stored messages found.  Try changing MG_EMAIL_TO to an address that stores messages and try again.")
}

func TestSendMGPlain(t *testing.T) {
	spendMoney(t, func() {
		toUser := reqEnv(t, "MG_EMAIL_TO")
		mg, err := NewMailgunFromEnv()
		ensure.Nil(t, err)

		m := mg.NewMessage(fromUser, exampleSubject, exampleText, toUser)
		msg, id, err := mg.Send(m)
		ensure.Nil(t, err)
		t.Log("TestSendPlain:MSG(" + msg + "),ID(" + id + ")")
	})
}

func TestSendMGPlainWithTracking(t *testing.T) {
	spendMoney(t, func() {
		toUser := reqEnv(t, "MG_EMAIL_TO")
		mg, err := NewMailgunFromEnv()
		ensure.Nil(t, err)

		m := mg.NewMessage(fromUser, exampleSubject, exampleText, toUser)
		m.SetTracking(true)
		msg, id, err := mg.Send(m)
		ensure.Nil(t, err)
		t.Log("TestSendPlainWithTracking:MSG(" + msg + "),ID(" + id + ")")
	})
}

func TestSendMGPlainAt(t *testing.T) {
	spendMoney(t, func() {
		toUser := reqEnv(t, "MG_EMAIL_TO")
		mg, err := NewMailgunFromEnv()
		ensure.Nil(t, err)

		m := mg.NewMessage(fromUser, exampleSubject, exampleText, toUser)
		m.SetDeliveryTime(time.Now().Add(5 * time.Minute))
		msg, id, err := mg.Send(m)
		ensure.Nil(t, err)
		t.Log("TestSendPlainAt:MSG(" + msg + "),ID(" + id + ")")
	})
}

func TestSendMGHtml(t *testing.T) {
	spendMoney(t, func() {
		toUser := reqEnv(t, "MG_EMAIL_TO")
		mg, err := NewMailgunFromEnv()
		ensure.Nil(t, err)

		m := mg.NewMessage(fromUser, exampleSubject, exampleText, toUser)
		m.SetHtml(exampleHtml)
		msg, id, err := mg.Send(m)
		ensure.Nil(t, err)
		t.Log("TestSendHtml:MSG(" + msg + "),ID(" + id + ")")
	})
}

func TestSendMGTracking(t *testing.T) {
	spendMoney(t, func() {
		toUser := reqEnv(t, "MG_EMAIL_TO")
		mg, err := NewMailgunFromEnv()
		ensure.Nil(t, err)

		m := mg.NewMessage(fromUser, exampleSubject, exampleText+"Tracking!\n", toUser)
		m.SetTracking(false)
		msg, id, err := mg.Send(m)
		ensure.Nil(t, err)
		t.Log("TestSendTracking:MSG(" + msg + "),ID(" + id + ")")
	})
}

func TestSendMGTag(t *testing.T) {
	spendMoney(t, func() {
		toUser := reqEnv(t, "MG_EMAIL_TO")
		mg, err := NewMailgunFromEnv()
		ensure.Nil(t, err)

		m := mg.NewMessage(fromUser, exampleSubject, exampleText+"Tags Galore!\n", toUser)
		m.AddTag("FooTag")
		m.AddTag("BarTag")
		m.AddTag("BlortTag")
		msg, id, err := mg.Send(m)
		ensure.Nil(t, err)
		t.Log("TestSendTag:MSG(" + msg + "),ID(" + id + ")")
	})
}

func TestSendMGMIME(t *testing.T) {
	spendMoney(t, func() {
		toUser := reqEnv(t, "MG_EMAIL_TO")
		mg, err := NewMailgunFromEnv()
		ensure.Nil(t, err)

		m := mg.NewMIMEMessage(ioutil.NopCloser(strings.NewReader(exampleMime)), toUser)
		msg, id, err := mg.Send(m)
		ensure.Nil(t, err)
		t.Log("TestSendMIME:MSG(" + msg + "),ID(" + id + ")")
	})
}

func TestSendMGBatchFailRecipients(t *testing.T) {
	spendMoney(t, func() {
		toUser := reqEnv(t, "MG_EMAIL_TO")
		mg, err := NewMailgunFromEnv()
		ensure.Nil(t, err)

		m := mg.NewMessage(fromUser, exampleSubject, exampleText+"Batch\n")
		for i := 0; i < MaxNumberOfRecipients; i++ {
			m.AddRecipient("") // We expect this to indicate a failure at the API
		}
		err = m.AddRecipientAndVariables(toUser, nil)
		// In case of error the SDK didn't send the message,
		// OR the API didn't check for empty To: headers.
		ensure.NotNil(t, err)
	})
}

func TestSendMGBatchRecipientVariables(t *testing.T) {
	spendMoney(t, func() {
		toUser := reqEnv(t, "MG_EMAIL_TO")
		mg, err := NewMailgunFromEnv()
		ensure.Nil(t, err)

		m := mg.NewMessage(fromUser, exampleSubject, templateText)
		err = m.AddRecipientAndVariables(toUser, map[string]interface{}{
			"name":  "Joe Cool Example",
			"table": 42,
		})
		ensure.Nil(t, err)
		_, _, err = mg.Send(m)
		ensure.Nil(t, err)
	})
}

func TestSendMGOffline(t *testing.T) {
	const (
		exampleDomain       = "testDomain"
		exampleAPIKey       = "testAPIKey"
		examplePublicAPIKey = "testPublicAPIKey"
		toUser              = "test@test.com"
		exampleMessage      = "Queue. Thank you"
		exampleID           = "<20111114174239.25659.5817@samples.mailgun.org>"
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ensure.DeepEqual(t, req.Method, http.MethodPost)
		ensure.DeepEqual(t, req.URL.Path, fmt.Sprintf("/%s/messages", exampleDomain))
		values, err := parseContentType(req)
		ensure.Nil(t, err)
		ensure.True(t, len(values) != 0)
		ensure.DeepEqual(t, values.Get("from"), fromUser)
		ensure.DeepEqual(t, values.Get("subject"), exampleSubject)
		ensure.DeepEqual(t, values.Get("text"), exampleText)
		ensure.DeepEqual(t, values.Get("to"), toUser)
		rsp := fmt.Sprintf(`{"message":"%s", "id":"%s"}`, exampleMessage, exampleID)
		fmt.Fprint(w, rsp)
	}))
	defer srv.Close()

	mg := NewMailgun(exampleDomain, exampleAPIKey, examplePublicAPIKey)
	mg.SetAPIBase(srv.URL)

	m := NewMessage(fromUser, exampleSubject, exampleText, toUser)
	msg, id, err := mg.Send(m)
	ensure.Nil(t, err)
	ensure.DeepEqual(t, msg, exampleMessage)
	ensure.DeepEqual(t, id, exampleID)
}

func TestSendMGSeparateDomain(t *testing.T) {
	const (
		exampleDomain = "testDomain"
		signingDomain = "signingDomain"

		exampleAPIKey       = "testAPIKey"
		examplePublicAPIKey = "testPublicAPIKey"
		toUser              = "test@test.com"
		exampleMessage      = "Queue. Thank you"
		exampleID           = "<20111114174239.25659.5817@samples.mailgun.org>"
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ensure.DeepEqual(t, req.Method, http.MethodPost)
		ensure.DeepEqual(t, req.URL.Path, fmt.Sprintf("/%s/messages", signingDomain))
		values, err := parseContentType(req)
		ensure.Nil(t, err)
		ensure.True(t, len(values) != 0)
		ensure.DeepEqual(t, values.Get("from"), fromUser)
		ensure.DeepEqual(t, values.Get("subject"), exampleSubject)
		ensure.DeepEqual(t, values.Get("text"), exampleText)
		ensure.DeepEqual(t, values.Get("to"), toUser)
		rsp := fmt.Sprintf(`{"message":"%s", "id":"%s"}`, exampleMessage, exampleID)
		fmt.Fprint(w, rsp)
	}))
	defer srv.Close()

	mg := NewMailgun(exampleDomain, exampleAPIKey, examplePublicAPIKey)
	mg.SetAPIBase(srv.URL)

	m := NewMessage(fromUser, exampleSubject, exampleText, toUser)
	m.AddDomain(signingDomain)

	msg, id, err := mg.Send(m)
	ensure.Nil(t, err)
	ensure.DeepEqual(t, msg, exampleMessage)
	ensure.DeepEqual(t, id, exampleID)
}
