// Copyright (c) 2016 Paul Jolly <paul@myitcv.org.uk>, all rights reserved.
// Use of this document is governed by a license found in the LICENSE document.

package react

// FooterElem is the React element definition corresponding to the HTML <footer> element
type FooterElem struct {
	Element
}

// _FooterProps are the props for a <footer> component
type _FooterProps struct {
	*BasicHTMLElement
}

// Footer creates a new instance of a <footer> element with the provided props and children
func Footer(props *FooterProps, children ...Element) *FooterElem {

	rProps := &_FooterProps{
		BasicHTMLElement: newBasicHTMLElement(),
	}

	if props != nil {
		props.assign(rProps)
	}

	return &FooterElem{
		Element: createElement("footer", rProps, children...),
	}
}
