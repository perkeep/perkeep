// +build js

package react_test

import (
	"testing"

	"honnef.co/go/js/dom"

	"myitcv.io/react"
	"myitcv.io/react/testutils"
)

func TestAElem(t *testing.T) {
	class := "test"

	x := testutils.Wrapper(react.A(&react.AProps{ClassName: class}))
	cont := testutils.RenderIntoDocument(x)

	el := testutils.FindRenderedDOMComponentWithClass(cont, class)

	if _, ok := el.(*dom.HTMLAnchorElement); !ok {
		t.Fatal("Failed to find <a> element")
	}
}
