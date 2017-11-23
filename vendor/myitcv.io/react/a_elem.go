// Copyright (c) 2016 Paul Jolly <paul@myitcv.org.uk>, all rights reserved.
// Use of this document is governed by a license found in the LICENSE document.

package react

// AElem is the React element definition corresponding to the HTML <a> element
type AElem struct {
	Element
}

func (a *AElem) coreReactElement() {}

// _AProps defines the properties for the <a> element
type _AProps struct {
	*BasicHTMLElement

	Title  string `js:"title"`
	Target string `js:"target"`
	Href   string `js:"href"`
}

// A creates a new instance of a <a> element with the provided props and
// children
func A(props *AProps, children ...Element) *AElem {

	rProps := &_AProps{
		BasicHTMLElement: newBasicHTMLElement(),
	}

	if props != nil {
		props.assign(rProps)
	}

	return &AElem{
		Element: createElement("a", rProps, children...),
	}
}
