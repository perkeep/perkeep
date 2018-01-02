/*
Copyright 2014 The Perkeep Authors

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

package storage

import (
	"reflect"
	"strings"
	"testing"
)

var tc *Client

func TestParseContainers(t *testing.T) {
	res := `<?xml version="1.0" encoding="utf-8"?>
<EnumerationResults ServiceEndpoint="https://myaccount.blob.core.windows.net/">
  <MaxResults>2</MaxResults>
  <Containers>
    <Container>
      <Name>containerOne</Name>
      <Properties>
        <Last-Modified>Wed, 23 Oct 2013 20:39:39 GMT</Last-Modified>
        <Etag>0x8CACB9BD7C6B1B2</Etag>
      </Properties>
    </Container>
    <Container>
      <Name>containerTwo</Name>
      <Properties>
        <Last-Modified>Wed, 23 Oct 2013 20:39:39 GMT</Last-Modified>
        <Etag>0x8CACB9BD7C1EEEC</Etag>
      </Properties>
    </Container>
  </Containers>
  <NextMarker>containerThree</NextMarker>
</EnumerationResults>`
	containers, err := parseListAllMyContainers(strings.NewReader(res))
	if err != nil {
		t.Fatal(err)
	}
	if g, w := len(containers), 2; g != w {
		t.Errorf("num parsed containers = %d; want %d", g, w)
	}
	want := []*Container{
		{Name: "containerOne"},
		{Name: "containerTwo"},
	}
	dump := func(v []*Container) {
		for i, b := range v {
			t.Logf("Container #%d: %#v", i, b)
		}
	}
	if !reflect.DeepEqual(containers, want) {
		t.Error("mismatch; GOT:")
		dump(containers)
		t.Error("WANT:")
		dump(want)
	}
}

func TestValidContainerNames(t *testing.T) {
	m := []struct {
		in   string
		want bool
	}{
		{"myazurecontainer", true},
		{"my-azure-container", true},
		{"myazurecontainer-1", true},
		{"my.azure.container", false},
		{"my---container.1", false},
		{".myazurecontainer", false},
		{"-myazurecontainer", false},
		{"myazurecontainer.", false},
		{"myazurecontainer-", false},
		{"my..myazurecontainer", false},
	}

	for _, bt := range m {
		got := IsValidContainer(bt.in)
		if got != bt.want {
			t.Errorf("func(%q) = %v; want %v", bt.in, got, bt.want)
		}
	}
}
