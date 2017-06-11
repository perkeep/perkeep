// Copyright (c) 2016 Paul Jolly <paul@myitcv.org.uk>, all rights reserved.
// Use of this document is governed by a license found in the LICENSE document.

package react

// PElem is the React element definition corresponding to the HTML <p> element
type PElem struct {
	Element
}

// _PProps are the props for a <div> component
type _PProps struct {
	*BasicHTMLElement
}

// P creates a new instance of a <p> element with the provided props and
// children
func P(props *PProps, children ...Element) *PElem {

	rProps := &_PProps{
		BasicHTMLElement: newBasicHTMLElement(),
	}

	if props != nil {
		props.assign(rProps)
	}

	return &PElem{
		Element: createElement("p", rProps, children...),
	}
}
