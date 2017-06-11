// Copyright (c) 2016 Paul Jolly <paul@myitcv.org.uk>, all rights reserved.
// Use of this document is governed by a license found in the LICENSE document.

package react

// NavElem is the React element definition corresponding to the HTML <nav> element
type NavElem struct {
	Element
}

// _NavProps defines the properties for the <nav> element
type _NavProps struct {
	*BasicHTMLElement
}

// Nav creates a new instance of a <nav> element with the provided props and children
func Nav(props *NavProps, children ...Element) *NavElem {

	rProps := &_NavProps{
		BasicHTMLElement: newBasicHTMLElement(),
	}

	if props != nil {
		props.assign(rProps)
	}

	return &NavElem{
		Element: createElement("nav", rProps, children...),
	}
}
