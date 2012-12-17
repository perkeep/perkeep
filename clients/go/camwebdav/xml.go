// +build THIS_IS_BROKEN

package main

import (
	"encoding/xml"
	"io"
	"log"
	"net/http"
)

func parsexml(r io.Reader) *xmlparser {
	x := &xmlparser{p: xml.NewDecoder(r)}
	x.next()
	return x
}

type xmlparser struct {
	p   *xml.Decoder
	cur xml.Token
}

// next moves to the next token,
// skipping anything that is not an element
// in the DAV: namespace
func (x *xmlparser) next() xml.Token {
	var err error
	for {
		x.cur, err = x.p.Token()
		if err == io.EOF {
			return x.cur
		} else if err != nil {
			panic(sendHTTPStatus(http.StatusBadRequest))
		}
		switch tok := x.cur.(type) {
		case xml.StartElement:
			if tok.Name.Space != "DAV:" {
				err = x.p.Skip()
				if err != nil && err != io.EOF {
					panic(sendHTTPStatus(http.StatusBadRequest))
				}
			} else {
				return x.cur
			}
		case xml.EndElement:
			return x.cur
		}
	}
	panic("unreachable")
}

func (x *xmlparser) start(name string) bool {
	el, ok := x.cur.(xml.StartElement)
	if !ok || el.Name.Local != name {
		return false
	}
	x.next()
	return true
}

func (x *xmlparser) muststart(name string) {
	if !x.start(name) {
		log.Printf("expected start element %q", name)
		panic(sendHTTPStatus(http.StatusBadRequest))
	}
}

func (x *xmlparser) end(name string) bool {
	if _, ok := x.cur.(xml.EndElement); !ok {
		return false
	}
	x.next()
	return true
}

func (x *xmlparser) mustend(name string) {
	if !x.end(name) {
		log.Printf("expected end element %q", name)
		panic(sendHTTPStatus(http.StatusBadRequest))
	}
}
