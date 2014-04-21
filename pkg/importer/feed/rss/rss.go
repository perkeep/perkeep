// Copyright 2012 Evan Farrer. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package rss defines XML data structures for an RSS feed.
package rss

type RSS struct {
	XMLName       string  `xml:"rss"`
	Title         string  `xml:"channel>title"`
	Link          []Link  `xml:"channel>link"`
	Description   string  `xml:"channel>description"`
	PubDate       string  `xml:"channel>pubDate,omitempty"`
	LastBuildDate string  `xml:"channel>lastBuildDate,omitempty"`
	Items         []*Item `xml:"channel>item"`
}

func (r *RSS) BaseLink() string {
	for _, l := range r.Link {
		if l.Rel == "" && l.Type == "" && l.Href == "" && l.Chardata != "" {
			return l.Chardata
		}
	}
	return ""
}

type Link struct {
	Rel      string `xml:"rel,attr"`
	Href     string `xml:"href,attr"`
	Type     string `xml:"type,attr"`
	Chardata string `xml:",chardata"`
}

type Item struct {
	Title       string        `xml:"title,omitempty"`
	Link        string        `xml:"link,omitempty"`
	Description string        `xml:"description,omitempty"`
	Author      string        `xml:"author,omitempty"`
	Enclosure   *Enclosure    `xml:"enclosure"`
	Guid        *Guid         `xml:"guid"`
	PubDate     string        `xml:"pubDate,omitempty"`
	Source      *Source       `xml:"source"`
	Content     string        `xml:"encoded,omitempty"`
	Date        string        `xml:"date,omitempty"`
	Published   string        `xml:"published,omitempty"`
	Media       *MediaContent `xml:"content"`
}

type MediaContent struct {
	XMLBase string `xml:"http://search.yahoo.com/mrss/ content"`
	URL     string `xml:"url,attr"`
	Type    string `xml:"type,attr"`
}

type Source struct {
	Source string `xml:",chardata"`
	Url    string `xml:"url,attr"`
}

type Guid struct {
	Guid        string `xml:",chardata"`
	IsPermaLink bool   `xml:"isPermaLink,attr,omitempty"`
}

type Enclosure struct {
	Url    string `xml:"url,attr"`
	Length string `xml:"length,attr,omitempty"`
	Type   string `xml:"type,attr"`
}
