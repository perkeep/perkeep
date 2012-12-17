// +build THIS_IS_BROKEN

package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"text/template"
	"time"
)

type xmler interface {
	XML(b *bytes.Buffer)
}

// See: http://www.webdav.org/specs/rfc4918.html

// 14.7 href XML Element
type href url.URL

func (h *href) XML(b *bytes.Buffer) {
	b.WriteString("<href>" + template.HTMLEscapeString((*url.URL)(h).String()) + "</href>")
}

// 14.16 multistatus XML Element
type multistatus []*response

func (m multistatus) XML(b *bytes.Buffer) {
	b.WriteString("<multistatus xmlns='DAV:'>")
	for _, r := range m {
		r.XML(b)
	}
	b.WriteString("</multistatus>")
}

// 14.24 response XML Element
type response struct {
	href *href
	body xmler // hrefsstatus OR propstats
}

func (r *response) XML(b *bytes.Buffer) {
	b.WriteString("<response>")
	r.href.XML(b)
	r.body.XML(b)
	b.WriteString("</response>")
}

// part of 14.24 response XML element

type hrefsstatus struct {
	hrefs  []*href
	status status
}

func (hs *hrefsstatus) XML(b *bytes.Buffer) {
	for _, h := range hs.hrefs {
		h.XML(b)
	}
	hs.status.XML(b)
}

// part of 14.24 response element

type propstats []propstat

func (p propstats) XML(b *bytes.Buffer) {
	b.WriteString("<propstat>")
	for _, prop := range p {
		prop.XML(b)
	}
	b.WriteString("</propstat>")
}

// 14.22 propstat XML Element
type propstat struct {
	props  []xmler
	status status
}

func (p *propstat) XML(b *bytes.Buffer) {
	b.WriteString("<prop>")
	for _, prop := range p.props {
		prop.XML(b)
	}
	b.WriteString("</prop>")
	p.status.XML(b)
}

// 14.28 status XML element
type status int

func (s status) XML(b *bytes.Buffer) {
	b.WriteString(fmt.Sprintf("<status>HTTP/1.1 %d %s</status>", s, template.HTMLEscapeString(http.StatusText(int(s)))))
}

// 15.1 creationdate Property
type creationdate uint64 // seconds from unix epoch

func (c creationdate) XML(b *bytes.Buffer) {
	b.WriteString("<creationdate>")
	b.WriteString(epochToXMLTime(int64(c)))
	b.WriteString("</creationdate>")
}

// 15.4 getcontentlength Property
type getcontentlength uint64

func (l getcontentlength) XML(b *bytes.Buffer) {

	b.WriteString("<getcontentlength>")
	b.WriteString(fmt.Sprint(l))
	b.WriteString("</getcontentlength>")
}

// 15.7 getlastmodified Property
type getlastmodified uint64 // seconds from unix epoch
func (g getlastmodified) XML(b *bytes.Buffer) {
	b.WriteString("<getlastmodified>")
	b.WriteString(epochToXMLTime(int64(g)))
	b.WriteString("</getlastmodified>")
}

// 15.9 resourcetype Property
type resourcetype bool // true if collection (directory), false otherwise

func (r resourcetype) XML(b *bytes.Buffer) {
	b.WriteString("<resourcetype>")
	if r {
		b.WriteString("<collection/>")
	}
	b.WriteString("</resourcetype>")
}

// helpers
func epochToXMLTime(sec int64) string {
	return template.HTMLEscapeString(time.Unix(sec, 0).UTC().Format(time.RFC3339))
}
