package react

import "github.com/gopherjs/gopherjs/js"

type FormDef struct {
	underlying *js.Object
}

type FormPropsDef struct {
	*BasicHTMLElement
}

func FormProps(f func(p *FormPropsDef)) *FormPropsDef {
	res := &FormPropsDef{
		BasicHTMLElement: newBasicHTMLElement(),
	}
	f(res)
	return res
}

func (d *FormDef) reactElement() {}

func Form(props *FormPropsDef, children ...Element) *FormDef {
	args := []interface{}{"form", props}

	for _, v := range children {
		args = append(args, elementToReactObj(v))
	}

	underlying := react.Call("createElement", args...)

	return &FormDef{underlying: underlying}
}
