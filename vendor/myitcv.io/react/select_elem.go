// Copyright (c) 2016 Paul Jolly <paul@myitcv.org.uk>, all rights reserved.
// Use of this document is governed by a license found in the LICENSE document.

package react

// SelectElem is the React element definition corresponding to the HTML <select> element
type SelectElem struct {
	Element
}

// _SelectProps are the props for a <select> component
type _SelectProps struct {
	*BasicHTMLElement

	Value string `js:"value"`
}

// Select creates a new instance of a <select> element with the provided props and children
func Select(props *SelectProps, children ...*OptionElem) *SelectElem {

	rProps := &_SelectProps{
		BasicHTMLElement: newBasicHTMLElement(),
	}

	if props != nil {
		props.assign(rProps)
	}

	var elems []Element
	for _, v := range children {
		elems = append(elems, v)
	}

	return &SelectElem{
		Element: createElement("select", rProps, elems...),
	}
}
