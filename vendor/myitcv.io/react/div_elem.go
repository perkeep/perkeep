// Copyright (c) 2016 Paul Jolly <paul@myitcv.org.uk>, all rights reserved.
// Use of this document is governed by a license found in the LICENSE document.

package react

// DivElem is the React element definition corresponding to the HTML <div> element
type DivElem struct {
	Element
}

// _DivProps are the props for a <div> component
type _DivProps struct {
	*BasicHTMLElement
}

// Div creates a new instance of a <div> element with the provided props and children
func Div(props *DivProps, children ...Element) *DivElem {

	rProps := &_DivProps{
		BasicHTMLElement: newBasicHTMLElement(),
	}

	if props != nil {
		props.assign(rProps)
	}

	return &DivElem{
		Element: createElement("div", rProps, children...),
	}
}
