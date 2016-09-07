// Copyright 2016 Google Inc. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package iterator_test

import (
	"bytes"
	"fmt"
	"html/template"
	"log"
	"net/http"

	"golang.org/x/net/context"
	"google.golang.org/api/iterator"
)

var (
	client *Client
	ctx    = context.Background()
)

var pageTemplate = template.Must(template.New("").Parse(`
<table>
  {{range .Entries}}
    <tr><td>{{.}}</td></tr>
  {{end}}
</table>
{{with .Next}}
  <a href="/entries?pageToken={{.}}">Next Page</a>
{{end}}
`))

// This example demonstrates how to use Pager to support
// pagination on a web site.
func Example_webHandler(w http.ResponseWriter, r *http.Request) {
	const pageSize = 25
	it := client.Items(ctx)
	var items []int
	pageToken, err := iterator.NewPager(it, pageSize, r.URL.Query().Get("pageToken")).NextPage(&items)
	if err != nil {
		http.Error(w, fmt.Sprintf("getting next page: %v", err), http.StatusInternalServerError)
	}
	data := struct {
		Items []int
		Next  string
	}{
		items,
		pageToken,
	}
	var buf bytes.Buffer
	if err := pageTemplate.Execute(&buf, data); err != nil {
		http.Error(w, fmt.Sprintf("executing page template: %v", err), http.StatusInternalServerError)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := buf.WriteTo(w); err != nil {
		log.Printf("writing response: %v", err)
	}
}

// This example demonstrates how to use a Pager to page through an iterator in a loop.
func Example_pageLoop() {
	const pageSize = 25
	ctx := context.Background()
	p := iterator.NewPager(client.Items(ctx), pageSize, "" /* start from the beginning */)
	for {
		var items []int
		pageToken, err := p.NextPage(&items)
		if err != nil {
			// TODO: handle error.
		}
		fmt.Println(items)
		if pageToken == "" {
			break
		}
	}
}

// This example demonstrates how to get exactly the items in the buffer, without
// triggering an extra RPC.
func Example_serverPages() {
	it := client.Items(ctx)
	var items []int
	for {
		item, err := it.Next()
		if err != nil && err != iterator.Done {
			log.Fatal(err)
		}
		if err == iterator.Done {
			break
		}
		items = append(items, item)
		if it.PageInfo().Remaining() == 0 {
			break
		}
	}
	fmt.Println(items)
}
