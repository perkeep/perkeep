// +build js

package react_test

import (
	"testing"

	"honnef.co/go/js/dom"

	"myitcv.io/react"
	"myitcv.io/react/testutils"
)

func TestTableElem(t *testing.T) {
	class := "test"

	x := testutils.Wrapper(react.Table(&react.TableProps{ClassName: class}))
	cont := testutils.RenderIntoDocument(x)

	el := testutils.FindRenderedDOMComponentWithClass(cont, class)

	if _, ok := el.(*dom.HTMLTableElement); !ok {
		t.Fatal("Failed to find <table> element")
	}
}
