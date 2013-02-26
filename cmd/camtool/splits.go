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

package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"camlistore.org/pkg/rollsum"
)

type span struct {
	from, to int64
	bits     int
	children []span
}

func showSplits(file string) {
	f, err := os.Open(file)
	if err != nil {
		panic(err.Error())
	}
	bufr := bufio.NewReader(f)

	spans := []span{}
	rs := rollsum.New()
	n := int64(0)
	last := n

	for {
		c, err := bufr.ReadByte()
		if err != nil {
			if err == io.EOF {
				if n != last {
					spans = append(spans, span{from: last, to: n})
				}
				break
			}
			panic(err.Error())
		}
		n++
		rs.Roll(c)
		if rs.OnSplit() {
			bits := rs.Bits()
			sliceFrom := len(spans)
			for sliceFrom > 0 && spans[sliceFrom-1].bits < bits {
				sliceFrom--
			}
			nCopy := len(spans) - sliceFrom
			var children []span
			if nCopy > 0 {
				children = make([]span, nCopy)
				nCopied := copy(children, spans[sliceFrom:])
				if nCopied != nCopy {
					panic("n wrong")
				}
				spans = spans[:sliceFrom]
			}
			spans = append(spans, span{from: last, to: n, bits: bits, children: children})

			log.Printf("split at %d (after %d), bits=%d", n, n-last, bits)
			last = n
		}
	}

	var dumpSpans func(s []span, indent int)
	dumpSpans = func(s []span, indent int) {
		in := strings.Repeat(" ", indent)
		for _, sp := range s {
			fmt.Printf("%sfrom=%d, to=%d (len %d) bits=%d\n", in, sp.from, sp.to, sp.to-sp.from, sp.bits)
			if len(sp.children) > 0 {
				dumpSpans(sp.children, indent+4)
			}
		}
	}
	dumpSpans(spans, 0)
	fmt.Printf("\n\nNOTE NOTE NOTE: the camdebug tool hasn't been updated to use the splitting policy from pkg/schema/filewriter.go.")
}
