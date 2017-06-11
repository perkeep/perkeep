// Copyright (c) 2016 Paul Jolly <paul@myitcv.org.uk>, all rights reserved.
// Use of this document is governed by a license found in the LICENSE document.

package react

// LiElem is the React element definition corresponding to the HTML <li> element
type LiElem struct {
	Element
}

// _LiProps defines the properties for the <li> element
type _LiProps struct {
	*BasicHTMLElement
}

// Li creates a new instance of an <li> element with the provided props
// and children
func Li(props *LiProps, children ...Element) *LiElem {

	rProps := &_LiProps{
		BasicHTMLElement: newBasicHTMLElement(),
	}

	if props != nil {
		props.assign(rProps)
	}

	return &LiElem{
		Element: createElement("li", rProps, children...),
	}
}
