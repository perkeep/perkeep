package testutils

import (
	"fmt"

	"honnef.co/go/js/dom"

	"myitcv.io/react"
	"myitcv.io/react/internal/core"

	"github.com/gopherjs/gopherjs/js"
)

var (
	reactObj     *js.Object
	addonsObj    *js.Object
	testUtilsObj *js.Object
)

func init() {
	reactObj = js.Global.Get("React")
	addonsObj = reactObj.Get("addons")
	testUtilsObj = addonsObj.Get("TestUtils")

	if testUtilsObj == nil || testUtilsObj == js.Undefined {
		panic(fmt.Errorf("Could not load React TestUtils - ensure you are using a development build"))
	}
}

func RenderIntoDocument(elem react.Element) *core.ElementHolder {
	v := testUtilsObj.Call("renderIntoDocument", elem)

	return &core.ElementHolder{
		Elem: v,
	}
}

func FindRenderedDOMComponentWithClass(elem react.Element, class string) dom.HTMLElement {
	return dom.WrapHTMLElement(testUtilsObj.Call("findRenderedDOMComponentWithClass", elem, class))
}
