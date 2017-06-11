// Copyright (c) 2016 Paul Jolly <paul@myitcv.org.uk>, all rights reserved.
// Use of this document is governed by a license found in the LICENSE document.

package react

// H4Elem is the React element definition corresponding to the HTML <h4> element
type H4Elem struct {
	Element
}

// _H4Props defines the properties for the <h4> element
type _H4Props struct {
	*BasicHTMLElement
}

// H4 creates a new instance of a <h4> element with the provided props and
// children
func H4(props *H4Props, children ...Element) *H4Elem {

	rProps := &_H4Props{
		BasicHTMLElement: newBasicHTMLElement(),
	}

	if props != nil {
		props.assign(rProps)
	}

	return &H4Elem{
		Element: createElement("h4", rProps, children...),
	}
}
