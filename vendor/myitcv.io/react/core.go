// Copyright (c) 2016 Paul Jolly <paul@myitcv.org.uk>, all rights reserved.
// Use of this document is governed by a license found in the LICENSE document.

package react

import (
	"github.com/gopherjs/gopherjs/js"
	"honnef.co/go/js/dom"
)

type AriaSet map[string]string
type DataSet map[string]string

type SyntheticEvent struct {
	o *js.Object

	PreventDefault  func() `js:"preventDefault"`
	StopPropagation func() `js:"stopPropagation"`
}

func (s *SyntheticEvent) Target() dom.HTMLElement {
	return dom.WrapHTMLElement(s.o.Get("target"))
}

type SyntheticMouseEvent struct {
	*SyntheticEvent

	ClientX int `js:"clientX"`
}

type RendersLi interface {
	Element
	RendersLi(*LiElem)
}

type Event interface{}

type Ref interface {
	Ref(h *js.Object)
}

type OnChange interface {
	Event

	OnChange(e *SyntheticEvent)
}

type OnClick interface {
	Event

	OnClick(e *SyntheticMouseEvent)
}
