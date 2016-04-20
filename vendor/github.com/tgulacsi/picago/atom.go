// Copyright 2014 Tamás Gulácsi. All rights reserved.
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package picago

import (
	"encoding/xml"
	"io"
	"time"
)

type Atom struct {
	ID           string    `xml:"id"`
	Name         string    `xml:"name"`
	Updated      time.Time `xml:"updated"`
	Title        string    `xml:"title"`
	Subtitle     string    `xml:"subtitle"`
	Icon         string    `xml:"icon"`
	Thumbnail    string    `xml:"http://schemas.google.com/photos/2007 thumbnail"`
	Author       Author    `xml:"author"`
	NumPhotos    int       `xml:"numphotos"`
	StartIndex   int       `xml:"startIndex"`
	TotalResults int       `xml:"totalResults"`
	ItemsPerPage int       `xml:"itemsPerPage"`
	Entries      []Entry   `xml:"entry"`
}

type Entry struct {
	ETag      string       `xml:"etag,attr"`
	EntryID   string       `xml:"http://www.w3.org/2005/Atom id"`
	ID        string       `xml:"http://schemas.google.com/photos/2007 id"`
	Published time.Time    `xml:"published"`
	Updated   time.Time    `xml:"updated"`
	Name      string       `xml:"http://schemas.google.com/photos/2007 name"`
	Title     string       `xml:"title"`
	Summary   string       `xml:"summary"`
	Links     []Link       `xml:"link"`
	Author    Author       `xml:"author"`
	Location  string       `xml:"http://schemas.google.com/photos/2007 location"`
	NumPhotos int          `xml:"numphotos"`
	Content   EntryContent `xml:"content"`
	Media     *Media       `xml:"group"`
	Exif      *Exif        `xml:"tags"`
	Point     string       `xml:"where>Point>pos"`
}

type Exif struct {
	FStop       float32 `xml:"fstop"`
	Make        string  `xml:"make"`
	Model       string  `xml:"model"`
	Exposure    float32 `xml:"exposure"`
	Flash       bool    `xml:"flash"`
	FocalLength float32 `xml:"focallength"`
	ISO         int32   `xml:"iso"`
	Timestamp   int64   `xml:"time"`
	UID         string  `xml:"imageUniqueID"`
}

type Link struct {
	Rel  string `xml:"rel,attr"`
	Type string `xml:"type,attr"`
	URL  string `xml:"href,attr"`
}

type Media struct {
	Title       string         `xml:"http://search.yahoo.com/mrss title"`
	Description string         `xml:"description"`
	Keywords    string         `xml:"keywords"`
	Content     []MediaContent `xml:"content"`
	Thumbnail   []MediaContent `xml:"thumbnail"`
}

type MediaContent struct {
	URL    string `xml:"url,attr"`
	Type   string `xml:"type,attr"`
	Width  int    `xml:"width,attr"`
	Height int    `xml:"height,attr"`
	Medium string `xml:"medium,attr"` // "image" or "video" for Picasa at least
}

type EntryContent struct {
	URL  string `xml:"src,attr"`
	Type string `xml:"type,attr"`
}

type Author struct {
	Name string `xml:"name"`
	URI  string `xml:"uri"`
}

func ParseAtom(r io.Reader) (*Atom, error) {
	result := new(Atom)
	if err := xml.NewDecoder(r).Decode(result); err != nil {
		return nil, err
	}
	return result, nil
}
