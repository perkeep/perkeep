/*
Copyright 2017 The Camlistore Authors.

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
	"bytes"
	"errors"
	"fmt"
	_ "image/png"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"rsc.io/pdf"
)

// pageCountPoppler calls the tool pdfinfo from the Poppler project for
// filename and parses the output for the page count which is then returned. If
// any error occurs or the pages count information is not found, an error is
// returned.
func pageCountPoppler(filename string) (cnt int, err error) {
	cmd := exec.Command("pdfinfo", filename)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return cnt, fmt.Errorf("could not get page count with pdfinfo: %v, %v", err, string(out))
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		l := sc.Text()
		if !strings.HasPrefix(l, "Pages: ") {
			continue
		}
		return strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(l, "Pages:")))
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	return 0, errors.New("page count not found in pdfinfo output")
}

// pageCountNative uses a native Go library to extract and return the number of
// pages in the PDF document. It returns an error if the filename is not of a
// PDF file, or if it failed to decode the PDF.
func pageCountNative(filename string) (int, error) {
	f, err := os.Open(filename)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return 0, err
	}
	p, err := pdf.NewReader(f, fi.Size())
	if err != nil {
		return 0, err
	}
	c := p.NumPage()
	if c < 1 {
		return 0, errors.New("encountered PDF without any pages")
	}
	return p.NumPage(), nil
}

// pageCount returns the number of pages in the given PDF file, or an error if
// it could not be determined. The function may trigger calls to external tools
// to achieve a valid count after native counting has been tried without
// success.
func pageCount(filename string) (int, error) {
	n, err := pageCountNative(filename)
	if err != nil {
		// fallback to using pdfinfo when internal count failed
		n, err = pageCountPoppler(filename)
	}
	return n, err
}
