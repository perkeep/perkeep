// Copyright (c) 2016 Paul Jolly <paul@myitcv.org.uk>, all rights reserved.
// Use of this document is governed by a license found in the LICENSE document.

package react

// ButtonElem is the React element definition corresponding to the HTML <button> element
type ButtonElem struct {
	Element
}

// _ButtonProps defines the properties for the <button> element
type _ButtonProps struct {
	*BasicHTMLElement

	Type string `js:"type"`
}

// Button creates a new instance of a <button> element with the provided props
// and child
func Button(props *ButtonProps, children ...Element) *ButtonElem {

	rProps := &_ButtonProps{
		BasicHTMLElement: newBasicHTMLElement(),
	}

	if props != nil {
		props.assign(rProps)
	}

	return &ButtonElem{
		Element: createElement("button", rProps, children...),
	}
}
