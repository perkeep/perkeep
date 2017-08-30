// Copyright (c) 2016 Paul Jolly <paul@myitcv.org.uk>, all rights reserved.
// Use of this document is governed by a license found in the LICENSE document.

package react

// BrElem is the React element definition corresponding to the HTML <br> element
type BrElem struct {
	Element
}

// _BrProps defines the properties for the <br> element
type _BrProps struct {
	*BasicHTMLElement
}

// Br creates a new instance of a <br> element with the provided props
func Br(props *BrProps) *BrElem {

	rProps := &_BrProps{
		BasicHTMLElement: newBasicHTMLElement(),
	}

	if props != nil {
		props.assign(rProps)
	}

	return &BrElem{
		Element: createElement("br", rProps),
	}
}
