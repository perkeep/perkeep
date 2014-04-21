/*
Copyright 2014 The Camlistore Authors

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

package feed

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"html"
	"log"
	"net/url"
	"strings"
	"time"

	"camlistore.org/pkg/importer/feed/atom"
	"camlistore.org/pkg/importer/feed/rdf"
	"camlistore.org/pkg/importer/feed/rss"
	"camlistore.org/third_party/code.google.com/p/go-charset/charset"
	_ "camlistore.org/third_party/code.google.com/p/go-charset/data"
)

type feed struct {
	Title   string
	Updated time.Time
	Link    string
	Items   []*item
}

type item struct {
	ID           string
	Title        string
	Link         string
	Created      time.Time
	Published    time.Time
	Updated      time.Time
	Author       string
	Content      string
	MediaContent string
}

func parseFeed(body []byte, feedURL string) (*feed, error) {
	var f *feed
	var atomerr, rsserr, rdferr error
	f, atomerr = parseAtom(body)
	if f == nil {
		f, rsserr = parseRSS(body)
	}
	if f == nil {
		f, rdferr = parseRDF(body)
	}
	if f == nil {
		log.Printf("atom parse error: %s", atomerr.Error())
		log.Printf("xml parse error: %s", rsserr.Error())
		log.Printf("rdf parse error: %s", rdferr.Error())
		return nil, fmt.Errorf("Could not parse feed data")
	}
	return f, nil
}

func parseAtom(body []byte) (*feed, error) {
	var f feed
	var a atom.Feed
	d := xml.NewDecoder(bytes.NewReader(body))
	d.CharsetReader = charset.NewReader
	if err := d.Decode(&a); err != nil {
		return nil, err
	}
	f.Title = a.Title
	if t, err := parseDate(string(a.Updated)); err == nil {
		f.Updated = t
	}
	fb, err := url.Parse(a.XMLBase)
	if err != nil {
		fb, _ = url.Parse("")
	}
	if len(a.Link) > 0 {
		f.Link = findBestAtomLink(a.Link)
		if l, err := fb.Parse(f.Link); err == nil {
			f.Link = l.String()
		}
	}

	for _, i := range a.Entry {
		eb, err := fb.Parse(i.XMLBase)
		if err != nil {
			eb = fb
		}
		st := item{
			ID:    i.ID,
			Title: atomTitle(i.Title),
		}
		if t, err := parseDate(string(i.Updated)); err == nil {
			st.Updated = t
		}
		if t, err := parseDate(string(i.Published)); err == nil {
			st.Published = t
		}
		if len(i.Link) > 0 {
			st.Link = findBestAtomLink(i.Link)
			if l, err := eb.Parse(st.Link); err == nil {
				st.Link = l.String()
			}
		}
		if i.Author != nil {
			st.Author = i.Author.Name
		}
		if i.Content != nil {
			if len(strings.TrimSpace(i.Content.Body)) != 0 {
				st.Content = i.Content.Body
			} else if len(i.Content.InnerXML) != 0 {
				st.Content = i.Content.InnerXML
			}
		} else if i.Summary != nil {
			st.Content = i.Summary.Body
		}
		f.Items = append(f.Items, &st)
	}
	return &f, nil
}

func parseRSS(body []byte) (*feed, error) {
	var f feed
	var r rss.RSS
	d := xml.NewDecoder(bytes.NewReader(body))
	d.CharsetReader = charset.NewReader
	d.DefaultSpace = "DefaultSpace"
	if err := d.Decode(&r); err != nil {
		return nil, err
	}
	f.Title = r.Title
	if t, err := parseDate(r.LastBuildDate, r.PubDate); err == nil {
		f.Updated = t
	}
	f.Link = r.BaseLink()

	for _, i := range r.Items {
		st := item{
			Link:   i.Link,
			Author: i.Author,
		}
		if i.Content != "" {
			st.Content = i.Content
		} else if i.Description != "" {
			st.Content = i.Description
		}
		if i.Title != "" {
			st.Title = i.Title
		} else if i.Description != "" {
			st.Title = i.Description
		}
		if st.Content == st.Title {
			st.Title = ""
		}
		st.Title = textTitle(st.Title)
		if i.Guid != nil {
			st.ID = i.Guid.Guid
		}
		if i.Enclosure != nil && strings.HasPrefix(i.Enclosure.Type, "audio/") {
			st.MediaContent = i.Enclosure.Url
		} else if i.Media != nil && strings.HasPrefix(i.Media.Type, "audio/") {
			st.MediaContent = i.Media.URL
		}
		if t, err := parseDate(i.PubDate, i.Date, i.Published); err == nil {
			st.Published = t
			st.Updated = t
		}
		f.Items = append(f.Items, &st)
	}

	return &f, nil
}

func parseRDF(body []byte) (*feed, error) {
	var f feed
	var rd rdf.RDF
	d := xml.NewDecoder(bytes.NewReader(body))
	d.CharsetReader = charset.NewReader
	if err := d.Decode(&rd); err != nil {
		return nil, err
	}
	if rd.Channel != nil {
		f.Title = rd.Channel.Title
		f.Link = rd.Channel.Link
		if t, err := parseDate(rd.Channel.Date); err == nil {
			f.Updated = t
		}
	}

	for _, i := range rd.Item {
		st := item{
			ID:     i.About,
			Title:  textTitle(i.Title),
			Link:   i.Link,
			Author: i.Creator,
		}
		if len(i.Description) > 0 {
			st.Content = html.UnescapeString(i.Description)
		} else if len(i.Content) > 0 {
			st.Content = html.UnescapeString(i.Content)
		}
		if t, err := parseDate(i.Date); err == nil {
			st.Published = t
			st.Updated = t
		}
		f.Items = append(f.Items, &st)
	}

	return &f, nil
}

func textTitle(t string) string {
	return html.UnescapeString(t)
}

func atomTitle(t *atom.Text) string {
	if t == nil {
		return ""
	}
	if t.Type == "html" {
		// see: https://github.com/mjibson/goread/blob/59aec794f3ef87b36c1bac029438c33a6aa6d8d3/utils.go#L533
		//return html.UnescapeString(sanitizer.StripTags(t.Body))
	}
	return textTitle(t.Body)
}

func findBestAtomLink(links []atom.Link) string {
	getScore := func(l atom.Link) int {
		switch {
		case l.Rel == "hub":
			return 0
		case l.Rel == "alternate" && l.Type == "text/html":
			return 5
		case l.Type == "text/html":
			return 4
		case l.Rel == "self":
			return 2
		case l.Rel == "":
			return 3
		default:
			return 1
		}
	}

	var bestlink string
	bestscore := -1
	for _, l := range links {
		score := getScore(l)
		if score > bestscore {
			bestlink = l.Href
			bestscore = score
		}
	}

	return bestlink
}

func parseFix(f *feed, feedURL string) (*feed, error) {
	f.Link = strings.TrimSpace(f.Link)
	f.Title = html.UnescapeString(strings.TrimSpace(f.Title))

	if u, err := url.Parse(feedURL); err == nil {
		if ul, err := u.Parse(f.Link); err == nil {
			f.Link = ul.String()
		}
	}
	base, err := url.Parse(f.Link)
	if err != nil {
		log.Printf("unable to parse link: %v", f.Link)
	}

	var nss []*item
	now := time.Now()
	for _, s := range f.Items {
		s.Created = now
		s.Link = strings.TrimSpace(s.Link)
		if s.ID == "" {
			if s.Link != "" {
				s.ID = s.Link
			} else if s.Title != "" {
				s.ID = s.Title
			} else {
				log.Printf("item has no id: %v", s)
				continue
			}
		}
		// if a story doesn't have a link, see if its id is a URL
		if s.Link == "" {
			if u, err := url.Parse(s.ID); err == nil {
				s.Link = u.String()
			}
		}
		if base != nil && s.Link != "" {
			link, err := base.Parse(s.Link)
			if err == nil {
				s.Link = link.String()
			} else {
				log.Printf("unable to resolve link: %v", s.Link)
			}
		}
		nss = append(nss, s)
	}
	f.Items = nss

	return f, nil
}

var dateFormats = []string{
	"01-02-2006",
	"01/02/2006",
	"01/02/2006 - 15:04",
	"01/02/2006 15:04:05 MST",
	"01/02/2006 3:04 PM",
	"02-01-2006",
	"02/01/2006",
	"02.01.2006 -0700",
	"02/01/2006 - 15:04",
	"02.01.2006 15:04",
	"02/01/2006 15:04:05",
	"02.01.2006 15:04:05",
	"02-01-2006 15:04:05 MST",
	"02/01/2006 15:04 MST",
	"02 Jan 2006",
	"02 Jan 2006 15:04:05",
	"02 Jan 2006 15:04:05 -0700",
	"02 Jan 2006 15:04:05 MST",
	"02 Jan 2006 15:04:05 UT",
	"02 Jan 2006 15:04 MST",
	"02 Monday, Jan 2006 15:04",
	"06-1-2 15:04",
	"06/1/2 15:04",
	"1/2/2006",
	"1/2/2006 15:04:05 MST",
	"1/2/2006 3:04:05 PM",
	"1/2/2006 3:04:05 PM MST",
	"15:04 02.01.2006 -0700",
	"2006-01-02",
	"2006/01/02",
	"2006-01-02 00:00:00.0 15:04:05.0 -0700",
	"2006-01-02 15:04",
	"2006-01-02 15:04:05 -0700",
	"2006-01-02 15:04:05-07:00",
	"2006-01-02 15:04:05-0700",
	"2006-01-02 15:04:05 MST",
	"2006-01-02 15:04:05Z",
	"2006-01-02 at 15:04:05",
	"2006-01-02T15:04:05",
	"2006-01-02T15:04:05:00",
	"2006-01-02T15:04:05 -0700",
	"2006-01-02T15:04:05-07:00",
	"2006-01-02T15:04:05-0700",
	"2006-01-02T15:04:05:-0700",
	"2006-01-02T15:04:05-07:00:00",
	"2006-01-02T15:04:05Z",
	"2006-01-02T15:04-07:00",
	"2006-01-02T15:04Z",
	"2006-1-02T15:04:05Z",
	"2006-1-2",
	"2006-1-2 15:04:05",
	"2006-1-2T15:04:05Z",
	"2006 January 02",
	"2-1-2006",
	"2/1/2006",
	"2.1.2006 15:04:05",
	"2 Jan 2006",
	"2 Jan 2006 15:04:05 -0700",
	"2 Jan 2006 15:04:05 MST",
	"2 Jan 2006 15:04:05 Z",
	"2 January 2006",
	"2 January 2006 15:04:05 -0700",
	"2 January 2006 15:04:05 MST",
	"6-1-2 15:04",
	"6/1/2 15:04",
	"Jan 02, 2006",
	"Jan 02 2006 03:04:05PM",
	"Jan 2, 2006",
	"Jan 2, 2006 15:04:05 MST",
	"Jan 2, 2006 3:04:05 PM",
	"Jan 2, 2006 3:04:05 PM MST",
	"January 02, 2006",
	"January 02, 2006 03:04 PM",
	"January 02, 2006 15:04",
	"January 02, 2006 15:04:05 MST",
	"January 2, 2006",
	"January 2, 2006 03:04 PM",
	"January 2, 2006 15:04:05",
	"January 2, 2006 15:04:05 MST",
	"January 2, 2006, 3:04 p.m.",
	"January 2, 2006 3:04 PM",
	"Mon, 02 Jan 06 15:04:05 MST",
	"Mon, 02 Jan 2006",
	"Mon, 02 Jan 2006 15:04:05",
	"Mon, 02 Jan 2006 15:04:05 00",
	"Mon, 02 Jan 2006 15:04:05 -07",
	"Mon 02 Jan 2006 15:04:05 -0700",
	"Mon, 02 Jan 2006 15:04:05 --0700",
	"Mon, 02 Jan 2006 15:04:05 -07:00",
	"Mon, 02 Jan 2006 15:04:05 -0700",
	"Mon,02 Jan 2006 15:04:05 -0700",
	"Mon, 02 Jan 2006 15:04:05 GMT-0700",
	"Mon , 02 Jan 2006 15:04:05 MST",
	"Mon, 02 Jan 2006 15:04:05 MST",
	"Mon, 02 Jan 2006 15:04:05MST",
	"Mon, 02 Jan 2006, 15:04:05 MST",
	"Mon, 02 Jan 2006 15:04:05 MST -0700",
	"Mon, 02 Jan 2006 15:04:05 MST-07:00",
	"Mon, 02 Jan 2006 15:04:05 UT",
	"Mon, 02 Jan 2006 15:04:05 Z",
	"Mon, 02 Jan 2006 15:04 -0700",
	"Mon, 02 Jan 2006 15:04 MST",
	"Mon,02 Jan 2006 15:04 MST",
	"Mon, 02 Jan 2006 15 -0700",
	"Mon, 02 Jan 2006 3:04:05 PM MST",
	"Mon, 02 January 2006",
	"Mon,02 January 2006 14:04:05 MST",
	"Mon, 2006-01-02 15:04",
	"Mon, 2 Jan 06 15:04:05 -0700",
	"Mon, 2 Jan 06 15:04:05 MST",
	"Mon, 2 Jan 15:04:05 MST",
	"Mon, 2 Jan 2006",
	"Mon,2 Jan 2006",
	"Mon, 2 Jan 2006 15:04",
	"Mon, 2 Jan 2006 15:04:05",
	"Mon, 2 Jan 2006 15:04:05 -0700",
	"Mon, 2 Jan 2006 15:04:05-0700",
	"Mon, 2 Jan 2006 15:04:05 -0700 MST",
	"mon,2 Jan 2006 15:04:05 MST",
	"Mon 2 Jan 2006 15:04:05 MST",
	"Mon, 2 Jan 2006 15:04:05 MST",
	"Mon, 2 Jan 2006 15:04:05MST",
	"Mon, 2 Jan 2006 15:04:05 UT",
	"Mon, 2 Jan 2006 15:04 -0700",
	"Mon, 2 Jan 2006, 15:04 -0700",
	"Mon, 2 Jan 2006 15:04 MST",
	"Mon, 2, Jan 2006 15:4",
	"Mon, 2 Jan 2006 15:4:5 -0700 GMT",
	"Mon, 2 Jan 2006 15:4:5 MST",
	"Mon, 2 Jan 2006 3:04:05 PM -0700",
	"Mon, 2 January 2006",
	"Mon, 2 January 2006 15:04:05 -0700",
	"Mon, 2 January 2006 15:04:05 MST",
	"Mon, 2 January 2006, 15:04:05 MST",
	"Mon, 2 January 2006, 15:04 -0700",
	"Mon, 2 January 2006 15:04 MST",
	"Monday, 02 January 2006 15:04:05",
	"Monday, 02 January 2006 15:04:05 -0700",
	"Monday, 02 January 2006 15:04:05 MST",
	"Monday, 2 Jan 2006 15:04:05 -0700",
	"Monday, 2 Jan 2006 15:04:05 MST",
	"Monday, 2 January 2006 15:04:05 -0700",
	"Monday, 2 January 2006 15:04:05 MST",
	"Monday, January 02, 2006",
	"Monday, January 2, 2006",
	"Monday, January 2, 2006 03:04 PM",
	"Monday, January 2, 2006 15:04:05 MST",
	"Mon Jan 02 2006 15:04:05 -0700",
	"Mon, Jan 02,2006 15:04:05 MST",
	"Mon Jan 02, 2006 3:04 pm",
	"Mon Jan 2 15:04:05 2006 MST",
	"Mon Jan 2 15:04 2006",
	"Mon, Jan 2 2006 15:04:05 -0700",
	"Mon, Jan 2 2006 15:04:05 -700",
	"Mon, Jan 2, 2006 15:04:05 MST",
	"Mon, Jan 2 2006 15:04 MST",
	"Mon, Jan 2, 2006 15:04 MST",
	"Mon, January 02, 2006 15:04:05 MST",
	"Mon, January 02, 2006, 15:04:05 MST",
	"Mon, January 2 2006 15:04:05 -0700",
	"Updated January 2, 2006",
	time.ANSIC,
	time.RFC1123,
	time.RFC1123Z,
	time.RFC3339,
	time.RFC822,
	time.RFC822Z,
	time.RFC850,
	time.RubyDate,
	time.UnixDate,
}

func parseDate(ds ...string) (t time.Time, err error) {
	for _, d := range ds {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		for _, f := range dateFormats {
			if t, err = time.Parse(f, d); err == nil {
				return
			}
		}
	}
	err = fmt.Errorf("could not parse dates: %v", strings.Join(ds, ", "))
	return
}
