// Copyright (c) 2016 Paul Jolly <paul@myitcv.org.uk>, all rights reserved.
// Use of this document is governed by a license found in the LICENSE document.

package react

// CodeElem is the React element definition corresponding to the HTML <code> element
type CodeElem struct {
	Element
}

// _CodeProps defines the properties for the <code> element
type _CodeProps struct {
	*BasicHTMLElement
}

// Code creates a new instance of a <code> element with the provided props
func Code(props *CodeProps, children ...Element) *CodeElem {

	rProps := &_CodeProps{
		BasicHTMLElement: newBasicHTMLElement(),
	}

	if props != nil {
		props.assign(rProps)
	}

	return &CodeElem{
		Element: createElement("code", rProps, children...),
	}
}
