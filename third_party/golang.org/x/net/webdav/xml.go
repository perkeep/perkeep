// Copyright 2014 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package webdav

// The XML encoding is covered by Section 14.
// http://www.webdav.org/specs/rfc4918.html#xml.element.definitions

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"time"
)

// http://www.webdav.org/specs/rfc4918.html#ELEMENT_lockinfo
type lockInfo struct {
	XMLName   xml.Name  `xml:"lockinfo"`
	Exclusive *struct{} `xml:"lockscope>exclusive"`
	Shared    *struct{} `xml:"lockscope>shared"`
	Write     *struct{} `xml:"locktype>write"`
	Owner     owner     `xml:"owner"`
}

// http://www.webdav.org/specs/rfc4918.html#ELEMENT_owner
type owner struct {
	InnerXML string `xml:",innerxml"`
}

func readLockInfo(r io.Reader) (li lockInfo, status int, err error) {
	c := &countingReader{r: r}
	if err = xml.NewDecoder(c).Decode(&li); err != nil {
		if err == io.EOF {
			if c.n == 0 {
				// An empty body means to refresh the lock.
				// http://www.webdav.org/specs/rfc4918.html#refreshing-locks
				return lockInfo{}, 0, nil
			}
			err = errInvalidLockInfo
		}
		return lockInfo{}, http.StatusBadRequest, err
	}
	// We only support exclusive (non-shared) write locks. In practice, these are
	// the only types of locks that seem to matter.
	if li.Exclusive == nil || li.Shared != nil || li.Write == nil {
		return lockInfo{}, http.StatusNotImplemented, errUnsupportedLockInfo
	}
	return li, 0, nil
}

type countingReader struct {
	n int
	r io.Reader
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

func writeLockInfo(w io.Writer, token string, ld LockDetails) (int, error) {
	depth := "infinity"
	if ld.ZeroDepth {
		depth = "0"
	}
	timeout := ld.Duration / time.Second
	return fmt.Fprintf(w, "<?xml version=\"1.0\" encoding=\"utf-8\"?>\n"+
		"<D:prop xmlns:D=\"DAV:\"><D:lockdiscovery><D:activelock>\n"+
		"	<D:locktype><D:write/></D:locktype>\n"+
		"	<D:lockscope><D:exclusive/></D:lockscope>\n"+
		"	<D:depth>%s</D:depth>\n"+
		"	<D:owner>%s</D:owner>\n"+
		"	<D:timeout>Second-%d</D:timeout>\n"+
		"	<D:locktoken><D:href>%s</D:href></D:locktoken>\n"+
		"	<D:lockroot><D:href>%s</D:href></D:lockroot>\n"+
		"</D:activelock></D:lockdiscovery></D:prop>",
		depth, ld.OwnerXML, timeout, escape(token), escape(ld.Root),
	)
}

func escape(s string) string {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"', '&', '\'', '<', '>':
			b := bytes.NewBuffer(nil)
			xml.EscapeText(b, []byte(s))
			return b.String()
		}
	}
	return s
}

// Next returns the next token, if any, in the XML stream of d.
// RFC 4918 requires to ignore comments, processing instructions
// and directives.
// http://www.webdav.org/specs/rfc4918.html#property_values
// http://www.webdav.org/specs/rfc4918.html#xml-extensibility
func next(d *xml.Decoder) (xml.Token, error) {
	for {
		t, err := d.Token()
		if err != nil {
			return t, err
		}
		switch t.(type) {
		case xml.Comment, xml.Directive, xml.ProcInst:
			continue
		default:
			return t, nil
		}
	}
}

// http://www.webdav.org/specs/rfc4918.html#ELEMENT_prop (for propfind)
type propnames []xml.Name

// UnmarshalXML appends the property names enclosed within start to pn.
//
// It returns an error if start does not contain any properties or if
// properties contain values. Character data between properties is ignored.
func (pn *propnames) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	for {
		t, err := next(d)
		if err != nil {
			return err
		}
		switch t.(type) {
		case xml.EndElement:
			if len(*pn) == 0 {
				return fmt.Errorf("%s must not be empty", start.Name.Local)
			}
			return nil
		case xml.StartElement:
			name := t.(xml.StartElement).Name
			t, err = next(d)
			if err != nil {
				return err
			}
			if _, ok := t.(xml.EndElement); !ok {
				return fmt.Errorf("unexpected token %T", t)
			}
			*pn = append(*pn, name)
		}
	}
}

// http://www.webdav.org/specs/rfc4918.html#ELEMENT_propfind
type propfind struct {
	XMLName  xml.Name  `xml:"DAV: propfind"`
	Allprop  *struct{} `xml:"DAV: allprop"`
	Propname *struct{} `xml:"DAV: propname"`
	Prop     propnames `xml:"DAV: prop"`
	Include  propnames `xml:"DAV: include"`
}

func readPropfind(r io.Reader) (pf propfind, status int, err error) {
	c := countingReader{r: r}
	if err = xml.NewDecoder(&c).Decode(&pf); err != nil {
		if err == io.EOF {
			if c.n == 0 {
				// An empty body means to propfind allprop.
				// http://www.webdav.org/specs/rfc4918.html#METHOD_PROPFIND
				return propfind{Allprop: new(struct{})}, 0, nil
			}
			err = errInvalidPropfind
		}
		return propfind{}, http.StatusBadRequest, err
	}

	if pf.Allprop == nil && pf.Include != nil {
		return propfind{}, http.StatusBadRequest, errInvalidPropfind
	}
	if pf.Allprop != nil && (pf.Prop != nil || pf.Propname != nil) {
		return propfind{}, http.StatusBadRequest, errInvalidPropfind
	}
	if pf.Prop != nil && pf.Propname != nil {
		return propfind{}, http.StatusBadRequest, errInvalidPropfind
	}
	if pf.Propname == nil && pf.Allprop == nil && pf.Prop == nil {
		return propfind{}, http.StatusBadRequest, errInvalidPropfind
	}
	return pf, 0, nil
}

// Property represents a single DAV resource property as defined in RFC 4918.
// See http://www.webdav.org/specs/rfc4918.html#data.model.for.resource.properties
type Property struct {
	// XMLName is the fully qualified name that identifies this property.
	XMLName xml.Name

	// Lang is an optional xml:lang attribute.
	Lang string `xml:"xml:lang,attr,omitempty"`

	// InnerXML contains the XML representation of the property value.
	// See http://www.webdav.org/specs/rfc4918.html#property_values
	//
	// Property values of complex type or mixed-content must have fully
	// expanded XML namespaces or be self-contained with according
	// XML namespace declarations. They must not rely on any XML
	// namespace declarations within the scope of the XML document,
	// even including the DAV: namespace.
	InnerXML []byte `xml:",innerxml"`
}

// http://www.webdav.org/specs/rfc4918.html#ELEMENT_error
type xmlError struct {
	XMLName  xml.Name `xml:"DAV: error"`
	InnerXML []byte   `xml:",innerxml"`
}

// http://www.webdav.org/specs/rfc4918.html#ELEMENT_propstat
type propstat struct {
	Prop                []Property `xml:"DAV: prop>_ignored_"`
	Status              string     `xml:"DAV: status"`
	Error               *xmlError  `xml:"DAV: error"`
	ResponseDescription string     `xml:"DAV: responsedescription,omitempty"`
}

// http://www.webdav.org/specs/rfc4918.html#ELEMENT_response
type response struct {
	XMLName             xml.Name   `xml:"DAV: response"`
	Href                []string   `xml:"DAV: href"`
	Propstat            []propstat `xml:"DAV: propstat"`
	Status              string     `xml:"DAV: status,omitempty"`
	Error               *xmlError  `xml:"DAV: error"`
	ResponseDescription string     `xml:"DAV: responsedescription,omitempty"`
}

// MultistatusWriter marshals one or more Responses into a XML
// multistatus response.
// See http://www.webdav.org/specs/rfc4918.html#ELEMENT_multistatus
type multistatusWriter struct {
	// ResponseDescription contains the optional responsedescription
	// of the multistatus XML element. Only the latest content before
	// close will be emitted. Empty response descriptions are not
	// written.
	responseDescription string

	w   http.ResponseWriter
	enc *xml.Encoder
}

// Write validates and emits a DAV response as part of a multistatus response
// element.
//
// It sets the HTTP status code of its underlying http.ResponseWriter to 207
// (Multi-Status) and populates the Content-Type header. If r is the
// first, valid response to be written, Write prepends the XML representation
// of r with a multistatus tag. Callers must call close after the last response
// has been written.
func (w *multistatusWriter) write(r *response) error {
	switch len(r.Href) {
	case 0:
		return errInvalidResponse
	case 1:
		if len(r.Propstat) > 0 != (r.Status == "") {
			return errInvalidResponse
		}
	default:
		if len(r.Propstat) > 0 || r.Status == "" {
			return errInvalidResponse
		}
	}
	if w.enc == nil {
		w.w.Header().Add("Content-Type", "text/xml; charset=utf-8")
		w.w.WriteHeader(StatusMulti)
		_, err := fmt.Fprintf(w.w, `<?xml version="1.0" encoding="UTF-8"?>`)
		if err != nil {
			return err
		}
		w.enc = xml.NewEncoder(w.w)
		err = w.enc.EncodeToken(xml.StartElement{
			Name: xml.Name{
				Space: "DAV:",
				Local: "multistatus",
			},
			Attr: []xml.Attr{{
				Name:  xml.Name{Local: "xmlns"},
				Value: "DAV:",
			}},
		})
		if err != nil {
			return err
		}
	}
	return w.enc.Encode(r)
}

// Close completes the marshalling of the multistatus response. It returns
// an error if the multistatus response could not be completed. If both the
// return value and field enc of w are nil, then no multistatus response has
// been written.
func (w *multistatusWriter) close() error {
	if w.enc == nil {
		return nil
	}
	var end []xml.Token
	if w.responseDescription != "" {
		name := xml.Name{Space: "DAV:", Local: "responsedescription"}
		end = append(end,
			xml.StartElement{Name: name},
			xml.CharData(w.responseDescription),
			xml.EndElement{Name: name},
		)
	}
	end = append(end, xml.EndElement{
		Name: xml.Name{Space: "DAV:", Local: "multistatus"},
	})
	for _, t := range end {
		err := w.enc.EncodeToken(t)
		if err != nil {
			return err
		}
	}
	return w.enc.Flush()
}
