// Copyright (c) 2016 Paul Jolly <paul@myitcv.org.uk>, all rights reserved.
// Use of this document is governed by a license found in the LICENSE document.

package react

// HrElem is the React element definition corresponding to the HTML <hr> element
type HrElem struct {
	Element
}

// _HrProps defines the properties for the <hr> element
type _HrProps struct {
	*BasicHTMLElement
}

// Hr creates a new instance of a <hr> element with the provided props
func Hr(props *HrProps) *HrElem {

	rProps := &_HrProps{
		BasicHTMLElement: newBasicHTMLElement(),
	}

	if props != nil {
		props.assign(rProps)
	}

	return &HrElem{
		Element: createElement("hr", rProps),
	}
}
