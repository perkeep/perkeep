/*
Copyright 2026 The Perkeep Authors

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

package livejournal

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"slices"
)

// A Comment is a comment on an entry of a journal.
type Comment struct {
	ID       int // the journal's talk ID
	JItemID  int // the entry's item ID, or zero if unknown
	ParentID int // the comment replied to, or zero
	PosterID int // zero for anonymous
	Poster   string
	// State is "A" (visible), "S" (screened), "D" (deleted), "F"
	// (frozen), or "B" (marked as spam).
	State   string
	Subject string
	Body    string
	Date    string // in UTC, such as "2001-05-05T22:48:14Z"
	Props   map[string]string
}

// DTalkID returns the comment's number in its URLs, given its entry's.
func (c *Comment) DTalkID(e *Entry) int { return c.ID*256 + e.Anum }

type xmlExport struct {
	MaxID    int          `xml:"maxid"`
	Comments []xmlComment `xml:"comments>comment"`
	Users    []struct {
		ID   int    `xml:"id,attr"`
		User string `xml:"user,attr"`
	} `xml:"usermaps>usermap"`
}

type xmlComment struct {
	ID       int    `xml:"id,attr"`
	JItemID  int    `xml:"jitemid,attr"`
	ParentID int    `xml:"parentid,attr"`
	PosterID int    `xml:"posterid,attr"`
	State    string `xml:"state,attr"`
	Subject  string `xml:"subject"`
	Body     string `xml:"body"`
	Date     string `xml:"date"`
	Props    []struct {
		Name  string `xml:"name,attr"`
		Value string `xml:",chardata"`
	} `xml:"property"`
}

func (c *Client) exportComments(ctx context.Context, session, get string, start int) (*xmlExport, error) {
	u := fmt.Sprintf("%s/export_comments.bml?get=%s&startid=%d", c.BaseURL, get, start)
	var x xmlExport
	err := c.retry(ctx, func() error {
		req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
		if err != nil {
			return err
		}
		req.AddCookie(&http.Cookie{Name: "ljsession", Value: session})
		body, err := c.do(req)
		if err != nil {
			return err
		}
		x = xmlExport{}
		return xml.Unmarshal(fixText(body), &x)
	})
	if err != nil {
		return nil, fmt.Errorf("livejournal: export_comments %s from %d: %w", get, start, err)
	}
	return &x, nil
}

// Comments returns all the comments on the journal's entries, in order of
// ID, including deleted ones, which have no text. progress, if non-nil,
// is called after each page with how many comments have been read, and
// how many there are at most.
func (c *Client) Comments(ctx context.Context, progress func(done, max int)) ([]*Comment, error) {
	session, err := c.getSession(ctx)
	if err != nil {
		return nil, err
	}
	byID := map[int]*Comment{}
	users := map[int]string{}

	// First the metadata of all comments, and their posters' names.
	maxID := 0
	for start := 0; ; {
		x, err := c.exportComments(ctx, session, "comment_meta", start)
		if err != nil {
			return nil, err
		}
		maxID = x.MaxID
		last := 0
		for _, xc := range x.Comments {
			byID[xc.ID] = &Comment{ID: xc.ID, PosterID: xc.PosterID, State: state(xc.State)}
			last = max(last, xc.ID)
		}
		for _, u := range x.Users {
			users[u.ID] = u.User
		}
		if last == 0 || last >= maxID {
			break
		}
		start = last + 1
	}

	// Then their entries, parents, and text.
	for start, done := 0, 0; ; {
		x, err := c.exportComments(ctx, session, "comment_body", start)
		if err != nil {
			return nil, err
		}
		last := 0
		for _, xc := range x.Comments {
			last = max(last, xc.ID)
			cm := byID[xc.ID]
			if cm == nil {
				cm = &Comment{ID: xc.ID, PosterID: xc.PosterID, State: state(xc.State)}
				byID[xc.ID] = cm
			}
			cm.JItemID = xc.JItemID
			cm.ParentID = xc.ParentID
			cm.Subject = xc.Subject
			cm.Body = xc.Body
			cm.Date = xc.Date
			if xc.State != "" {
				cm.State = xc.State
			}
			for _, p := range xc.Props {
				if cm.Props == nil {
					cm.Props = map[string]string{}
				}
				cm.Props[p.Name] = p.Value
			}
		}
		done += len(x.Comments)
		if progress != nil {
			progress(done, len(byID))
		}
		if last == 0 || last >= maxID {
			break
		}
		start = last + 1
	}

	cs := make([]*Comment, 0, len(byID))
	for _, cm := range byID {
		cm.Poster = users[cm.PosterID]
		cs = append(cs, cm)
	}
	sortComments(cs)
	return cs, nil
}

// state returns the state of a comment in the export, where visible ones
// have none.
func state(s string) string {
	if s == "" {
		return "A"
	}
	return s
}

func sortComments(cs []*Comment) {
	slices.SortFunc(cs, func(a, b *Comment) int { return a.ID - b.ID })
}

func sortEntries(es []*Entry) {
	slices.SortFunc(es, func(a, b *Entry) int { return a.ItemID - b.ItemID })
}
