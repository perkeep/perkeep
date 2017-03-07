package react

import "github.com/gopherjs/gopherjs/js"

type DivDef struct {
	underlying *js.Object
}

type DivPropsDef struct {
	*BasicHTMLElement
}

func DivProps(f func(p *DivPropsDef)) *DivPropsDef {
	res := &DivPropsDef{
		BasicHTMLElement: newBasicHTMLElement(),
	}
	f(res)
	return res
}

func (d *DivDef) reactElement() {}

func Div(props *DivPropsDef, children ...Element) *DivDef {
	args := []interface{}{"div", props}

	for _, v := range children {
		args = append(args, elementToReactObj(v))
	}

	underlying := react.Call("createElement", args...)

	return &DivDef{underlying: underlying}
}
