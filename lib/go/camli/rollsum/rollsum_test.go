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

package rollsum

import (
	"rand"
	"testing"
)

func TestSum(t *testing.T) {
	var buf [100000]uint8
	rnd := rand.New(rand.NewSource(4))
	for i := range buf {
		buf[i] = uint8(rnd.Intn(256))
	}

	sum := func(offset, len int) uint32 {
		rs := New()
		for count := offset; count < len; count++ {
			rs.Roll(buf[count])
		}
		return rs.Digest()
	}

	sum1a := sum(0, len(buf))
	sum1b := sum(1, len(buf))
	sum2a := sum(len(buf)-windowSize*5/2, len(buf)-windowSize)
	sum2b := sum(0, len(buf)-windowSize)
	sum3a := sum(0, windowSize+3)
	sum3b := sum(3, windowSize+3)

	if sum1a != sum1b {
		t.Errorf("sum1a=%d sum1b=%d", sum1a, sum1b)
	}
	if sum2a != sum2b {
		t.Errorf("sum2a=%d sum2b=%d", sum2a, sum2b)
	}
	if sum3a != sum3b {
		t.Errorf("sum3a=%d sum3b=%d", sum3a, sum3b)
	}
}
