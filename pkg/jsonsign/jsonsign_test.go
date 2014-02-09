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

package jsonsign_test

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"testing"

	. "camlistore.org/pkg/jsonsign"
	"camlistore.org/pkg/test"
	. "camlistore.org/pkg/test/asserts"
	"camlistore.org/third_party/code.google.com/p/go.crypto/openpgp"
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

var pubKey2 = `-----BEGIN PGP PUBLIC KEY BLOCK-----
Version: GnuPG v1.4.10 (GNU/Linux)

mQENBEz61lcBCADRQhcb9LIQdV3LhU5f7cCjOctmLsL+y4k4VKmznssWORiNPEHQ
13CxFLjRDN2OQYXi4NSqoUqHNMsRTUJTVW0CnznUUb11ibXLUYW/zbPN9dWs8PlI
UZSScS1dxtGKKk+VfXrvc1LB6pqrjWmAgEwQxsBWToW2IFR/eMo1LiVU83dzpKU1
n/yb8Jy9wizchspd9xecK2X0JnKLRIJklLTAKQ+XKP+cSwXmShcs+3pxu5f4piqF
7oBfh9noFA0vdGYNBGVch3DfJwFcTmLkkGFZKdiehWncvVYT1jxUkJvc0K44ohDH
smkG2VZm3rJCwi2GIWA/clLiDAhYM6vTI3oZABEBAAG0K0NhbWxpIFRlc3R1c2Vy
IFR3byA8Y2FtbGkudGVzdEBleGFtcGxlLmNvbT6JATgEEwECACIFAkz61lcCGwMG
CwkIBwMCBhUIAgkKCwQWAgMBAh4BAheAAAoJEIUeCLJL7Fq1c44IAKOJjymoinXd
9NOW7GfpHCmynzSflJJoRcRzsNz83lJbwITYCd1ExQxkO84sMKRPJiefc9epP/Hg
8V4b1SwkGi+A8WaoH/OZtEM8HA7iEKmV+wjfZE6kt+y0trbxdu42W5hLz/uerrNl
G+r90mBNjmJXsZxmwaZEFrLtFlqezCzdQSur35QLZMFvW6aoYFTAgOk1rk9lBtkC
DePaadZQGHNWr+Rw2M5xXv9BZ4Rrjl6VLjE2DuqMSBVkelckBcsmRppaszF3J8y3
9gd10xC+5/LVfhU8niDZjY3pIcjQwsYJ+Jdyce2OEYo1i6pQDiq2WewXdCJ28DVK
1SX38WFB3Zm5AQ0ETPrWVwEIAMQ/dRCrkhy2D0SzJV5o/Z3uVf1nFLlEFfavV45F
8wtG/Bi5EuZXoYqU+O79O7sPy9Dw3Qhxtvt159l6/sSLXYTBBs3HJ2zTVhI5tbAZ
DMz4/wfkRP/h74KuXnWfin1ynswzqdPVXgrRvTsfHbkwbTaRwbx186VYqM17Wqy2
hFAUCdQIIW0+X9upjGek+kESldSzeUV87fr3IN/pq6fRc90h8xAKfz6mMc7AAUUL
NLNxb9y18u4Bw+fKgc6W7YxB+gQN1IajmgGPcqUTxNxydWF974iqsKnkZpzHg0Ce
zGGLWzCAGzI8drltgJPBoGGo56U1s2hW6JzLUi03phV10H8AEQEAAYkBHwQYAQIA
CQUCTPrWVwIbDAAKCRCFHgiyS+xatUPIB/9VPOeIxH5UcNYuZT+LW2tdcWPNhyQ+
u5UC9DC2A3F9AYNYRwDcSVOMmqS8hPJxg/biFxFoGFgm14Vp0nd1blOHcmNXcDzk
XTv2CKcUbgYpvDVmfCcEf6seSf+/RDbyj/VzebE6yvXuwsPus7ntbMw+Dum42z55
XYiYsfEFu25RtxritG3eYklCKymdRg615pj8zoRpL5Z1NAy5QBb5sv5hPbdGSyqL
Kw6aLcq2IU7kev6CYJVyXzJ1XtsYv/o7hzKKmZ5WcwuPc9Yqh6onJt1RC8jzz8Ry
jyVNPb8AaaWVW1uZLg6Em61aKnbOG10B30m3CQ8dwBjF9hgmtcY0IZ/Y
=OWHA
-----END PGP PUBLIC KEY BLOCK-----
`

var pubKeyBlob1 = &test.Blob{pubKey1} // user 1
var pubKeyBlob2 = &test.Blob{pubKey2} // user 2

var testFetcher = &test.Fetcher{}

func init() {
	testFetcher.AddBlob(pubKeyBlob1)
	testFetcher.AddBlob(pubKeyBlob2)
}

func TestSigningBadInput(t *testing.T) {
	sr := newRequest(1)

	sr.UnsignedJSON = ""
	_, err := sr.Sign()
	ExpectErrorContains(t, err, "json parse error", "empty input")

	sr.UnsignedJSON = "{}"
	_, err = sr.Sign()
	ExpectErrorContains(t, err, "json lacks \"camliSigner\" key", "just braces")

	sr.UnsignedJSON = `{"camliSigner": 123}`
	_, err = sr.Sign()
	ExpectErrorContains(t, err, "\"camliSigner\" key is malformed or unsupported", "camliSigner 123")

	sr.UnsignedJSON = `{"camliSigner": ""}`
	_, err = sr.Sign()
	ExpectErrorContains(t, err, "\"camliSigner\" key is malformed or unsupported", "empty camliSigner")
}

func newRequest(userN int) *SignRequest {
	if userN < 1 || userN > 2 {
		panic("invalid userid")
	}
	suffix := ".gpg"
	if userN == 2 {
		suffix = "2.gpg"
	}
	return &SignRequest{
		UnsignedJSON:      "",
		Fetcher:           testFetcher,
		ServerMode:        true,
		SecretKeyringPath: "./testdata/test-secring" + suffix,
	}
}

func TestSigning(t *testing.T) {
	sr := newRequest(1)
	sr.UnsignedJSON = fmt.Sprintf(`{"camliVersion": 1, "foo": "fooVal", "camliSigner": %q  }`, pubKeyBlob1.BlobRef().String())
	signed, err := sr.Sign()
	AssertNil(t, err, "no error signing")
	Assert(t, strings.Contains(signed, `"camliSig":`), "got a camliSig")

	vr := NewVerificationRequest(signed, testFetcher)
	if !vr.Verify() {
		t.Fatalf("verification failed on signed json [%s]: %v", signed, vr.Err)
	}
	ExpectString(t, "fooVal", vr.PayloadMap["foo"].(string), "PayloadMap")
	ExpectString(t, "2931A67C26F5ABDA", vr.SignerKeyId, "SignerKeyId")

	// Test a non-matching signature.
	fakeSigned := strings.Replace(signed, pubKeyBlob1.BlobRef().String(), pubKeyBlob2.BlobRef().String(), 1)
	vr = NewVerificationRequest(fakeSigned, testFetcher)
	if vr.Verify() {
		t.Fatalf("unexpected verification of faked signature")
	}
	AssertErrorContains(t, vr.Err, "openpgp: invalid signature: hash tag doesn't match",
		"expected signature verification error")

	t.Logf("TODO: verify GPG-vs-Go sign & verify interop both ways, once implemented.")
}

func TestEntityFromSecring(t *testing.T) {
	ent, err := EntityFromSecring("26F5ABDA", "testdata/test-secring.gpg")
	if err != nil {
		t.Fatalf("EntityFromSecring: %v", err)
	}
	if ent == nil {
		t.Fatalf("nil entity")
	}
	if _, ok := ent.Identities["Camli Tester <camli-test@example.com>"]; !ok {
		t.Errorf("missing expected identity")
	}
}

func TestWriteKeyRing(t *testing.T) {
	ent, err := EntityFromSecring("26F5ABDA", "testdata/test-secring.gpg")
	if err != nil {
		t.Fatalf("NewEntity: %v", err)
	}
	var buf bytes.Buffer
	err = WriteKeyRing(&buf, openpgp.EntityList([]*openpgp.Entity{ent}))
	if err != nil {
		t.Fatalf("WriteKeyRing: %v", err)
	}

	el, err := openpgp.ReadKeyRing(&buf)
	if err != nil {
		t.Fatalf("ReadKeyRing: %v", err)
	}
	if len(el) != 1 {
		t.Fatalf("ReadKeyRing read %d entities; want 1", len(el))
	}
	orig := entityString(ent)
	got := entityString(el[0])
	if orig != got {
		t.Fatalf("original vs. wrote-then-read entities differ:\norig: %s\n got: %s", orig, got)
	}
}

// stupid entity stringier for testing.
func entityString(ent *openpgp.Entity) string {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "PublicKey=%s", ent.PrimaryKey.KeyIdShortString())
	var ids []string
	for k := range ent.Identities {
		ids = append(ids, k)
	}
	sort.Strings(ids)
	for _, k := range ids {
		fmt.Fprintf(&buf, " id[%q]", k)
	}
	return buf.String()
}
