// Copyright (c) 2016 Paul Jolly <paul@myitcv.org.uk>, all rights reserved.
// Use of this document is governed by a license found in the LICENSE document.

package react

// TextAreaElem is the React element definition corresponding to the HTML <textarea> element
type TextAreaElem struct {
	Element
}

// _TextAreaProps defines the properties for the <textarea> element
type _TextAreaProps struct {
	*BasicHTMLElement

	Rows         uint   `js:"rows"`
	Cols         uint   `js:"cols"`
	Placeholder  string `js:"placeholder"`
	Value        string `js:"value"`
	DefaultValue string `js:"defaultValue" react:"omitempty"`
}

// TextArea creates a new instance of a <textarea> element with the provided props and
// children
func TextArea(props *TextAreaProps, children ...Element) *TextAreaElem {

	rProps := &_TextAreaProps{
		BasicHTMLElement: newBasicHTMLElement(),
	}

	if props != nil {
		props.assign(rProps)
	}

	return &TextAreaElem{
		Element: createElement("textarea", rProps, children...),
	}
}
