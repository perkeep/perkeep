// Copyright (c) 2016 Paul Jolly <paul@myitcv.org.uk>, all rights reserved.
// Use of this document is governed by a license found in the LICENSE document.

package react

// ImgElem is the React element definition corresponding to the HTML <Img> element
type ImgElem struct {
	Element
}

// _ImgProps are the props for a <Img> component
type _ImgProps struct {
	*BasicHTMLElement

	Src string `js:"src"`
}

// Img creates a new instance of a <Img> element with the provided props and children
func Img(props *ImgProps, children ...Element) *ImgElem {

	rProps := &_ImgProps{
		BasicHTMLElement: newBasicHTMLElement(),
	}

	if props != nil {
		props.assign(rProps)
	}

	return &ImgElem{
		Element: createElement("img", rProps, children...),
	}
}
