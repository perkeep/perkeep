/*
Copyright 2011 Google Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package jsonsign

import (
	"camli/blobref"
	. "camli/testing"

	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
)

var unsigned = `{"camliVersion": 1,
"camliType": "foo"
}`

var pubKey1 = `-----BEGIN PGP PUBLIC KEY BLOCK-----
Version: GnuPG v1.4.10 (GNU/Linux)

mQENBEzgoVsBCAC/56aEJ9BNIGV9FVP+WzenTAkg12k86YqlwJVAB/VwdMlyXxvi
bCT1RVRfnYxscs14LLfcMWF3zMucw16mLlJCBSLvbZ0jn4h+/8vK5WuAdjw2YzLs
WtBcjWn3lV6tb4RJz5gtD/o1w8VWxwAnAVIWZntKAWmkcChCRgdUeWso76+plxE5
aRYBJqdT1mctGqNEISd/WYPMgwnWXQsVi3x4z1dYu2tD9uO1dkAff12z1kyZQIBQ
rexKYRRRh9IKAayD4kgS0wdlULjBU98aeEaMz1ckuB46DX3lAYqmmTEL/Rl9cOI0
Enpn/oOOfYFa5h0AFndZd1blMvruXfdAobjVABEBAAG0JUNhbWxpIFRlc3RlciA8
Y2FtbGktdGVzdEBleGFtcGxlLmNvbT6JATgEEwECACIFAkzgoVsCGwMGCwkIBwMC
BhUIAgkKCwQWAgMBAh4BAheAAAoJECkxpnwm9avaHE0IAJ/pMZgiURl3kefrFMAV
7ei0XDfTekZOwDRcZWTVQ/A97phpzO8t78qLYbFeHuq3myNhrlVO9Gyp+2V904rN
dudoHLhpegf5TNeHGmAGHBxcooMPMp0JyIDnUBxtCNGxgWfbKpEDRsQAjkCc7sR0
H+OegzlEf6JZGzEhV5ohOioTsC1DmJNoQsRz5Kes7sLoAzpQCbCv4yv+1o+mnzgW
9qPJXKxcScc0t2YTvcvpJ7LV8no1OP6vpYqB1A9Pzze6XFBlcXOUKbRKk0fEIV/u
pU3ph1fF7wlyRgA4A3iPwDC4BgVmHYkz9nYPn+7IcT/dDig5SWU+n7WZgGeyv75y
0Ue5AQ0ETOChWwEIALuHxKI+oSH+eeMSXhxcSUXnhp4cUeyvOV7oNPYcmsDclF0Y
7y8NrSPiEZod9vSTEDMq7hd3BG+feCBqjgR4qtmoXguJhWcnJqDBk5iAMuuAph9O
CC8QLACMJPhoxQ0UtDPKlpG4X8kLK1woHd716ulPl2KLjTgd6K4kCGj+CV5Ekn6u
IJj+3IPbYDOwk1l06ksimwQAY4dA1CXOTviH1bVqR6CzuzVPg4hcryWDva1rEO5c
LcOR8Wk/thANFLSNjqX8UgtGXhFZRWxKetFDQiX5f2BKoqTVYvD3pqt+zzyLNFAz
xhMc3cyFfqM8yQdzdEey/DIWtMoDqZCSVMJ63N8AEQEAAYkBHwQYAQIACQUCTOCh
WwIbDAAKCRApMaZ8JvWr2mHACACkco+fAfRK+gmprF2m8E0Bp1frwFH0g4RJVHXQ
BUDbg7OZbWumzD4Br28si6XDVMP6fLOeyD0EHYb6LhAHDkBLqx6e3kKG1mQ8fMIV
O4YMQfskYH2FJqlCtgMnM8N3oslPBTpZedNPSUq7HJh2pKr9GIDi1V+Hgc/qEigE
dj9f2zSSaKZdC4eL73GvlQOh+4XqgaMnMiKfI+/2WlRaJs1KOgKmIp5yHt0qY0ef
y+40BY/z9pMjyUvr/Wwp8KXArw0NAwzp8NUl5fNxRg9XWQWLn6hW8ydR20X3t2ym
iNSWzNQiTT6k7fumOABCoSZsow/AJxQSxqKOJBjgpKjIKCgY
=ru0J
-----END PGP PUBLIC KEY BLOCK-----`

var pubKeyBlob1 = &blobref.TestBlob{pubKey1}

var testFetcher = &TestFetcher{}

func init() {
	testFetcher.AddBlob(pubKeyBlob1)
}

func TestSigningBadInput(t *testing.T) {
	sr := newRequest()

	sr.UnsignedJson = ""
	_, err := sr.Sign()
	ExpectErrorContains(t, err, "json parse error", "empty input")

	sr.UnsignedJson = "{}"
	_, err = sr.Sign()
	ExpectErrorContains(t, err, "json lacks \"camliSigner\" key", "just braces")

	sr.UnsignedJson = `{"camliSigner": 123}`
	_, err = sr.Sign()
	ExpectErrorContains(t, err, "\"camliSigner\" key is malformed or unsupported", "camliSigner 123")

	sr.UnsignedJson = `{"camliSigner": ""}`
	_, err = sr.Sign()
	ExpectErrorContains(t, err, "\"camliSigner\" key is malformed or unsupported", "empty camliSigner")
}

func newRequest() *SignRequest {
	return &SignRequest{
		UnsignedJson:      "",
		Fetcher:           testFetcher,
		UseAgent:          false,
		ServerMode:        true,
		SecretKeyringPath: "./testdata/test-secring.gpg",
		KeyringPath:       "./testdata/test-keyring.gpg",
	}
}

func TestSigning(t *testing.T) {
	sr := newRequest()
	sr.UnsignedJson = fmt.Sprintf(`{"camliVersion": 1, "camliSigner": %q  }`, pubKeyBlob1.BlobRef().String())
	signed, err := sr.Sign()
	AssertNil(t, err, "no error signing")
	Assert(t, strings.Contains(signed, `"camliSig":`), "got a camliSig")

	vr := NewVerificationRequest(signed, testFetcher)
	if !vr.Verify() {
		t.Errorf("verification failed on signed json [%s]: %v", signed, vr.Err)
	}
	t.Logf("TODO: finish these tests; verify things round-trip, verify GPG external-vs-Go sign & verify round-trip, test signatures from wrong signer don't verify, etc.")
}

type TestFetcher struct {
	l sync.Mutex
	m map[string]*blobref.TestBlob
}

func (tf *TestFetcher) AddBlob(b *blobref.TestBlob) {
	tf.l.Lock()
	defer tf.l.Unlock()
	if tf.m == nil {
		tf.m = make(map[string]*blobref.TestBlob)
	}
	tf.m[b.BlobRef().String()] = b
}

func (tf *TestFetcher) Fetch(ref *blobref.BlobRef) (file blobref.ReadSeekCloser, size int64, err os.Error) {
	tf.l.Lock()
	defer tf.l.Unlock()
	if tf.m == nil {
		err = os.ENOENT
		return
	}
	tb, ok := tf.m[ref.String()]
	if !ok {
		err = os.ENOENT
		return
	}
	file = &strReader{tb.Val, 0}
	size = int64(len(tb.Val))
	return
}

type strReader struct {
	s   string
	pos int
}

func (sr *strReader) Close() os.Error { return nil }

func (sr *strReader) Seek(offset int64, whence int) (ret int64, err os.Error) {
	// Note: ignoring 64-bit offsets.  test data should be tiny.
	switch whence {
	case 0:
		sr.pos = int(offset)
	case 1:
		sr.pos += int(offset)
	case 2:
		sr.pos = len(sr.s) + int(offset)
	}
	ret = int64(sr.pos)
	return
}

func (sr *strReader) Read(p []byte) (n int, err os.Error) {
	if sr.pos >= len(sr.s) {
		err = os.EOF
		return
	}
	n = copy(p, sr.s[sr.pos:])
	if n == 0 {
		err = os.EOF
	}
	sr.pos += n
	return
}
