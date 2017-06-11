// Copyright (c) 2016 Paul Jolly <paul@myitcv.org.uk>, all rights reserved.
// Use of this document is governed by a license found in the LICENSE document.

package react

// InputElem is the React element definition corresponding to the HTML <input> element
type InputElem struct {
	Element
}

// _InputProps defines the properties for the <input> element
type _InputProps struct {
	*BasicHTMLElement

	Placeholder  string `js:"placeholder"`
	Type         string `js:"type"`
	Value        string `js:"value"`
	DefaultValue string `js:"defaultValue" react:"omitempty"`
}

// Input creates a new instance of a <input> element with the provided props
func Input(props *InputProps) *InputElem {

	rProps := &_InputProps{
		BasicHTMLElement: newBasicHTMLElement(),
	}

	if props != nil {
		props.assign(rProps)
	}

	return &InputElem{
		Element: createElement("input", rProps),
	}
}
