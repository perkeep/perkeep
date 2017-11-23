// Copyright (c) 2016 Paul Jolly <paul@myitcv.org.uk>, all rights reserved.
// Use of this document is governed by a license found in the LICENSE document.

package react

// IFrameElem is the React element definition corresponding to the HTML <iframe> element
type IFrameElem struct {
	Element
}

// _IFrameProps are the props for a <iframe> component
type _IFrameProps struct {
	*BasicHTMLElement

	Src    string `js:"src"`
	SrcDoc string `js:"srcDoc"`
}

// IFrame creates a new instance of a <iframe> element with the provided props and children
func IFrame(props *IFrameProps, children ...Element) *IFrameElem {

	rProps := &_IFrameProps{
		BasicHTMLElement: newBasicHTMLElement(),
	}

	if props != nil {
		props.assign(rProps)
	}

	return &IFrameElem{
		Element: createElement("iframe", rProps, children...),
	}
}
