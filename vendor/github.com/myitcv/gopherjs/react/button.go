package react

import "github.com/gopherjs/gopherjs/js"

type ButtonDef struct {
	underlying *js.Object
}

type ButtonPropsDef struct {
	*BasicHTMLElement

	Type string `js:"type"`
}

func ButtonProps(f func(p *ButtonPropsDef)) *ButtonPropsDef {
	res := &ButtonPropsDef{
		BasicHTMLElement: newBasicHTMLElement(),
	}
	f(res)
	return res
}

func (d *ButtonDef) reactElement() {}

func Button(props *ButtonPropsDef, child Element) *ButtonDef {
	args := []interface{}{"button", props, elementToReactObj(child)}

	underlying := react.Call("createElement", args...)

	return &ButtonDef{
		underlying: underlying,
	}
}
