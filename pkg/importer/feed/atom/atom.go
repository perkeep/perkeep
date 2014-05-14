// Copyright 2009 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Adapted from encoding/xml/read_test.go.

// Package atom defines XML data structures for an Atom feed.
package atom

import (
	"encoding/xml"
	"time"
)

type Feed struct {
	XMLName xml.Name `xml:"feed"`
	Title   string   `xml:"title"`
	ID      string   `xml:"id"`
	Link    []Link   `xml:"link"`
	Updated TimeStr  `xml:"updated"`
	Author  *Person  `xml:"author"`
	Entry   []*Entry `xml:"entry"`
	XMLBase string   `xml:"base,attr"`
}

type Entry struct {
	Title     *Text   `xml:"title"`
	ID        string  `xml:"id"`
	Link      []Link  `xml:"link"`
	Published TimeStr `xml:"published"`
	Updated   TimeStr `xml:"updated"`
	Author    *Person `xml:"author"`
	Summary   *Text   `xml:"summary"`
	Content   *Text   `xml:"content"`
	XMLBase   string  `xml:"base,attr"`
}

type Link struct {
	Rel  string `xml:"rel,attr"`
	Href string `xml:"href,attr"`
	Type string `xml:"type,attr"`
}

type Person struct {
	Name     string `xml:"name"`
	URI      string `xml:"uri"`
	Email    string `xml:"email"`
	InnerXML string `xml:",innerxml"`
}

type Text struct {
	Type     string `xml:"type,attr"`
	Body     string `xml:",chardata"`
	InnerXML string `xml:",innerxml"`
}

type TimeStr string

func Time(t time.Time) TimeStr {
	return TimeStr(t.Format("2006-01-02T15:04:05-07:00"))
}
