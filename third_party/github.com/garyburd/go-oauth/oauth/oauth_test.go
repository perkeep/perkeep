// Copyright 2010 Gary Burd
//
// Licensed under the Apache License, Version 2.0 (the "License"): you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
// WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the
// License for the specific language governing permissions and limitations
// under the License.

package oauth

import (
	"bytes"
	"net/url"
	"testing"
)

func parseURL(urlStr string) *url.URL {
	u, _ := url.Parse(urlStr)
	return u
}

var oauthTests = []struct {
	method    string
	url       *url.URL
	appParams url.Values
	nonce     string
	timestamp string

	clientCredentials Credentials
	credentials       Credentials

	base   string
	header string
}{
	{
		// Simple example from Twitter OAuth tool
		"GET",
		parseURL("https://api.twitter.com/1/"),
		url.Values{"page": {"10"}},
		"8067e8abc6bdca2006818132445c8f4c",
		"1355795903",
		Credentials{"kMViZR2MHk2mM7hUNVw9A", "56Fgl58yOfqXOhHXX0ybvOmSnPQFvR2miYmm30A"},
		Credentials{"10212-JJ3Zc1A49qSMgdcAO2GMOpW9l7A348ESmhjmOBOU", "yF75mvq4LZMHj9O0DXwoC3ZxUnN1ptvieThYuOAYM"},
		`GET&https%3A%2F%2Fapi.twitter.com%2F1%2F&oauth_consumer_key%3DkMViZR2MHk2mM7hUNVw9A%26oauth_nonce%3D8067e8abc6bdca2006818132445c8f4c%26oauth_signature_method%3DHMAC-SHA1%26oauth_timestamp%3D1355795903%26oauth_token%3D10212-JJ3Zc1A49qSMgdcAO2GMOpW9l7A348ESmhjmOBOU%26oauth_version%3D1.0%26page%3D10`,
		`OAuth oauth_consumer_key="kMViZR2MHk2mM7hUNVw9A", oauth_nonce="8067e8abc6bdca2006818132445c8f4c", oauth_signature="o5cx1ggJrY9ognZuVVeUwglKV8U%3D", oauth_signature_method="HMAC-SHA1", oauth_timestamp="1355795903", oauth_token="10212-JJ3Zc1A49qSMgdcAO2GMOpW9l7A348ESmhjmOBOU", oauth_version="1.0"`,
	},
	{
		// Test case and port insensitivity.
		"GeT",
		parseURL("https://apI.twItter.com:443/1/"),
		url.Values{"page": {"10"}},
		"8067e8abc6bdca2006818132445c8f4c",
		"1355795903",
		Credentials{"kMViZR2MHk2mM7hUNVw9A", "56Fgl58yOfqXOhHXX0ybvOmSnPQFvR2miYmm30A"},
		Credentials{"10212-JJ3Zc1A49qSMgdcAO2GMOpW9l7A348ESmhjmOBOU", "yF75mvq4LZMHj9O0DXwoC3ZxUnN1ptvieThYuOAYM"},
		`GET&https%3A%2F%2Fapi.twitter.com%2F1%2F&oauth_consumer_key%3DkMViZR2MHk2mM7hUNVw9A%26oauth_nonce%3D8067e8abc6bdca2006818132445c8f4c%26oauth_signature_method%3DHMAC-SHA1%26oauth_timestamp%3D1355795903%26oauth_token%3D10212-JJ3Zc1A49qSMgdcAO2GMOpW9l7A348ESmhjmOBOU%26oauth_version%3D1.0%26page%3D10`,
		`OAuth oauth_consumer_key="kMViZR2MHk2mM7hUNVw9A", oauth_nonce="8067e8abc6bdca2006818132445c8f4c", oauth_signature="o5cx1ggJrY9ognZuVVeUwglKV8U%3D", oauth_signature_method="HMAC-SHA1", oauth_timestamp="1355795903", oauth_token="10212-JJ3Zc1A49qSMgdcAO2GMOpW9l7A348ESmhjmOBOU", oauth_version="1.0"`,
	},
	{
		// Example generated using the Netflix OAuth tool.
		"GET",
		parseURL("http://api-public.netflix.com/catalog/titles"),
		url.Values{"term": {"Dark Knight"}, "count": {"2"}},
		"1234",
		"1355850443",
		Credentials{"apiKey001", "sharedSecret002"},
		Credentials{"accessToken003", "accessSecret004"},
		`GET&http%3A%2F%2Fapi-public.netflix.com%2Fcatalog%2Ftitles&count%3D2%26oauth_consumer_key%3DapiKey001%26oauth_nonce%3D1234%26oauth_signature_method%3DHMAC-SHA1%26oauth_timestamp%3D1355850443%26oauth_token%3DaccessToken003%26oauth_version%3D1.0%26term%3DDark%2520Knight`,
		`OAuth oauth_consumer_key="apiKey001", oauth_nonce="1234", oauth_signature="0JAoaqt6oz6TJx8N%2B06XmhPjcOs%3D", oauth_signature_method="HMAC-SHA1", oauth_timestamp="1355850443", oauth_token="accessToken003", oauth_version="1.0"`,
	},
	{
		// Test special characters in form values.
		"GET",
		parseURL("http://PHOTOS.example.net:8001/Photos"),
		url.Values{"photo size": {"300%"}, "title": {"Back of $100 Dollars Bill"}},
		"kllo~9940~pd9333jh",
		"1191242096",
		Credentials{"dpf43f3++p+#2l4k3l03", "secret01"},
		Credentials{"nnch734d(0)0sl2jdk", "secret02"},
		"GET&http%3A%2F%2Fphotos.example.net%3A8001%2FPhotos&oauth_consumer_key%3Ddpf43f3%252B%252Bp%252B%25232l4k3l03%26oauth_nonce%3Dkllo~9940~pd9333jh%26oauth_signature_method%3DHMAC-SHA1%26oauth_timestamp%3D1191242096%26oauth_token%3Dnnch734d%25280%25290sl2jdk%26oauth_version%3D1.0%26photo%2520size%3D300%2525%26title%3DBack%2520of%2520%2524100%2520Dollars%2520Bill",
		`OAuth oauth_consumer_key="dpf43f3%2B%2Bp%2B%232l4k3l03", oauth_nonce="kllo~9940~pd9333jh", oauth_signature="n1UAoQy2PoIYizZUiWvkdCxM3P0%3D", oauth_signature_method="HMAC-SHA1", oauth_timestamp="1191242096", oauth_token="nnch734d%280%290sl2jdk", oauth_version="1.0"`,
	},
	{
		// Test special characters in path, multiple values for same key in form.
		"GET",
		parseURL("http://EXAMPLE.COM:80/Space%20Craft"),
		url.Values{"name": {"value", "value"}},
		"Ix4U1Ei3RFL",
		"1327384901",
		Credentials{"abcd", "efgh"},
		Credentials{"ijkl", "mnop"},
		"GET&http%3A%2F%2Fexample.com%2FSpace%2520Craft&name%3Dvalue%26name%3Dvalue%26oauth_consumer_key%3Dabcd%26oauth_nonce%3DIx4U1Ei3RFL%26oauth_signature_method%3DHMAC-SHA1%26oauth_timestamp%3D1327384901%26oauth_token%3Dijkl%26oauth_version%3D1.0",
		`OAuth oauth_consumer_key="abcd", oauth_nonce="Ix4U1Ei3RFL", oauth_signature="TZZ5u7qQorLnmKs%2Biqunb8gqkh4%3D", oauth_signature_method="HMAC-SHA1", oauth_timestamp="1327384901", oauth_token="ijkl", oauth_version="1.0"`,
	},
	{
		// Test with query string in URL.
		"GET",
		parseURL("http://EXAMPLE.COM:80/Space%20Craft?name=value"),
		url.Values{"name": {"value"}},
		"Ix4U1Ei3RFL",
		"1327384901",
		Credentials{"abcd", "efgh"},
		Credentials{"ijkl", "mnop"},
		"GET&http%3A%2F%2Fexample.com%2FSpace%2520Craft&name%3Dvalue%26name%3Dvalue%26oauth_consumer_key%3Dabcd%26oauth_nonce%3DIx4U1Ei3RFL%26oauth_signature_method%3DHMAC-SHA1%26oauth_timestamp%3D1327384901%26oauth_token%3Dijkl%26oauth_version%3D1.0",
		`OAuth oauth_consumer_key="abcd", oauth_nonce="Ix4U1Ei3RFL", oauth_signature="TZZ5u7qQorLnmKs%2Biqunb8gqkh4%3D", oauth_signature_method="HMAC-SHA1", oauth_timestamp="1327384901", oauth_token="ijkl", oauth_version="1.0"`,
	},
	{
		// Test "/" in form value.
		"POST",
		parseURL("https://stream.twitter.com/1.1/statuses/filter.json"),
		url.Values{"track": {"example.com/abcd"}},
		"bf2cb6d611e59f99103238fc9a3bb8d8",
		"1362434376",
		Credentials{"consumer_key", "consumer_secret"},
		Credentials{"token", "secret"},
		"POST&https%3A%2F%2Fstream.twitter.com%2F1.1%2Fstatuses%2Ffilter.json&oauth_consumer_key%3Dconsumer_key%26oauth_nonce%3Dbf2cb6d611e59f99103238fc9a3bb8d8%26oauth_signature_method%3DHMAC-SHA1%26oauth_timestamp%3D1362434376%26oauth_token%3Dtoken%26oauth_version%3D1.0%26track%3Dexample.com%252Fabcd",
		`OAuth oauth_consumer_key="consumer_key", oauth_nonce="bf2cb6d611e59f99103238fc9a3bb8d8", oauth_signature="LcxylEOnNdgoKSJi7jX07mxcvfM%3D", oauth_signature_method="HMAC-SHA1", oauth_timestamp="1362434376", oauth_token="token", oauth_version="1.0"`,
	},
	{
		// Test "/" in query string
		"POST",
		parseURL("https://stream.twitter.com/1.1/statuses/filter.json?track=example.com/query"),
		url.Values{},
		"884275759fbab914654b50ae643c563a",
		"1362435218",
		Credentials{"consumer_key", "consumer_secret"},
		Credentials{"token", "secret"},
		"POST&https%3A%2F%2Fstream.twitter.com%2F1.1%2Fstatuses%2Ffilter.json&oauth_consumer_key%3Dconsumer_key%26oauth_nonce%3D884275759fbab914654b50ae643c563a%26oauth_signature_method%3DHMAC-SHA1%26oauth_timestamp%3D1362435218%26oauth_token%3Dtoken%26oauth_version%3D1.0%26track%3Dexample.com%252Fquery",
		`OAuth oauth_consumer_key="consumer_key", oauth_nonce="884275759fbab914654b50ae643c563a", oauth_signature="OAldqvRrKDXRGZ9BqSi2CqeVH0g%3D", oauth_signature_method="HMAC-SHA1", oauth_timestamp="1362435218", oauth_token="token", oauth_version="1.0"`,
	},
}

func TestBaseString(t *testing.T) {
	for _, ot := range oauthTests {
		oauthParams := map[string]string{
			"oauth_consumer_key":     ot.clientCredentials.Token,
			"oauth_nonce":            ot.nonce,
			"oauth_timestamp":        ot.timestamp,
			"oauth_token":            ot.credentials.Token,
			"oauth_signature_method": "HMAC-SHA1",
			"oauth_version":          "1.0",
		}
		var buf bytes.Buffer
		writeBaseString(&buf, ot.method, ot.url, ot.appParams, oauthParams)
		base := buf.String()
		if base != ot.base {
			t.Errorf("base string for %s %s\n    = %q,\n want %q", ot.method, ot.url, base, ot.base)
		}
	}
}

func TestAuthorizationHeader(t *testing.T) {
	defer func() {
		testingNonce = ""
		testingTimestamp = ""
	}()
	for _, ot := range oauthTests {
		c := Client{Credentials: ot.clientCredentials}
		testingNonce = ot.nonce
		testingTimestamp = ot.timestamp
		header := c.AuthorizationHeader(&ot.credentials, ot.method, ot.url, ot.appParams)
		if header != ot.header {
			t.Errorf("authorization header for %s %s\ngot:  %s\nwant: %s", ot.method, ot.url, header, ot.header)
		}
	}
}
