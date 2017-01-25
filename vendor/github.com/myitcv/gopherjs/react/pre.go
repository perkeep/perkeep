package react

import "github.com/gopherjs/gopherjs/js"

type PreDef struct {
	underlying *js.Object
}

type PrePropsDef struct {
	*BasicHTMLElement
}

func PreProps(f func(p *PrePropsDef)) *PrePropsDef {
	res := &PrePropsDef{
		BasicHTMLElement: newBasicHTMLElement(),
	}
	f(res)
	return res
}

func (d *PreDef) reactElement() {}

func Pre(props *PrePropsDef, children ...Element) *PreDef {
	args := []interface{}{"pre", props}

	for _, v := range children {
		args = append(args, elementToReactObj(v))
	}

	underlying := react.Call("createElement", args...)

	return &PreDef{underlying: underlying}
}
