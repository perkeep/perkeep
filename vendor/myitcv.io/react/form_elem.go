// Copyright (c) 2016 Paul Jolly <paul@myitcv.org.uk>, all rights reserved.
// Use of this document is governed by a license found in the LICENSE document.

package react

// FormElem is the React element definition corresponding to the HTML <form> element
type FormElem struct {
	Element
}

// _FormProps defines the properties for the <form> element
type _FormProps struct {
	*BasicHTMLElement
}

// Form creates a new instance of a <form> element with the provided props and
// children
func Form(props *FormProps, children ...Element) *FormElem {

	rProps := &_FormProps{
		BasicHTMLElement: newBasicHTMLElement(),
	}

	if props != nil {
		props.assign(rProps)
	}

	return &FormElem{
		Element: createElement("form", rProps, children...),
	}
}
