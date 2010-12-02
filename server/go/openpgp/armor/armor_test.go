// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package armor

import (
	"testing"
	"fmt"
)

type GetLineTest struct {
	in, out1, out2 string
}

var getLineTests = []GetLineTest{
	GetLineTest{"abc", "abc", ""},
	GetLineTest{"abc\r", "abc\r", ""},
	GetLineTest{"abc\n", "abc", ""},
	GetLineTest{"abc\r\n", "abc", ""},
	GetLineTest{"abc\nd", "abc", "d"},
	GetLineTest{"abc\r\nd", "abc", "d"},
	GetLineTest{"\nabc", "", "abc"},
	GetLineTest{"\r\nabc", "", "abc"},
}

func TestGetLine(t *testing.T) {
	for i, test := range getLineTests {
		x, y := getLine([]byte(test.in))
		if string(x) != test.out1 || string(y) != test.out2 {
			t.Errorf("#%d got:%+v,%+v want:%s,%s", i, x, y, test.out1, test.out2)
		}
	}
}

func TestDecode(t *testing.T) {
	result, _ := Decode([]byte(armorExample1))
	fmt.Printf("%#v\n", result)
}

const armorExample1 = `-----BEGIN PGP SIGNATURE-----
Version: GnuPG v1.4.10 (GNU/Linux)

iQEcBAABAgAGBQJMtFESAAoJEKsQXJGvOPsVj40H/1WW6jaMXv4BW+1ueDSMDwM8
kx1fLOXbVM5/Kn5LStZNt1jWWnpxdz7eq3uiqeCQjmqUoRde3YbB2EMnnwRbAhpp
cacnAvy9ZQ78OTxUdNW1mhX5bS6q1MTEJnl+DcyigD70HG/yNNQD7sOPMdYQw0TA
byQBwmLwmTsuZsrYqB68QyLHI+DUugn+kX6Hd2WDB62DKa2suoIUIHQQCd/ofwB3
WfCYInXQKKOSxu2YOg2Eb4kLNhSMc1i9uKUWAH+sdgJh7NBgdoE4MaNtBFkHXRvv
okWuf3+xA9ksp1npSY/mDvgHijmjvtpRDe6iUeqfCn8N9u9CBg8geANgaG8+QA4=
=wfQG
-----END PGP SIGNATURE-----`
