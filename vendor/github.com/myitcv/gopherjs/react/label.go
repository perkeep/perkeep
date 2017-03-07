package react

import "github.com/gopherjs/gopherjs/js"

type LabelDef struct {
	underlying *js.Object
}

type LabelPropsDef struct {
	*BasicHTMLElement

	For string `js:"htmlFor"`
}

func LabelProps(f func(p *LabelPropsDef)) *LabelPropsDef {
	res := &LabelPropsDef{
		BasicHTMLElement: newBasicHTMLElement(),
	}
	f(res)
	return res
}

func (d *LabelDef) reactElement() {}

func Label(props *LabelPropsDef, child Element) *LabelDef {
	underlying := react.Call("createElement", "label", props, elementToReactObj(child))

	return &LabelDef{underlying: underlying}
}
