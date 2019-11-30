// Copyright (c) 2016 Paul Jolly <paul@myitcv.org.uk>, all rights reserved.
// Use of this document is governed by a license found in the LICENSE document.

package react

import "github.com/gopherjs/gopherjs/js"

// DangerousInnerHTML is convenience definition that allows HTML to be directly
// set as the child of a DOM element. See
// https://facebook.github.io/react/docs/dom-elements.html#dangerouslysetinnerhtml
// for more details
type DangerousInnerHTML struct {
	o *js.Object
}

// NewDangerousInnerHTML creates a new DangerousInnerHTML instance, using the
// supplied string as the raw HTML
func NewDangerousInnerHTML(s string) *DangerousInnerHTML {
	o := object.New()
	o.Set("__html", s)

	res := &DangerousInnerHTML{o: o}

	return res
}

func (d *DangerousInnerHTML) reactElement() {}

// FragmentElem is the special React Fragment element definition. Fragments let
// you group a list of children without adding extra nodes to the DOM. See
// https://reactjs.org/docs/fragments.html for more details.
type FragmentElem struct {
	Element
}

// Fragment creates a new instance of a <React.Fragment> element with the
// provided children
func Fragment(children ...Element) *FragmentElem {
	return &FragmentElem{
		Element: createElement(symbolFragment, nil, children...),
	}
}
