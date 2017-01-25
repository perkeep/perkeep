package react

import "github.com/gopherjs/gopherjs/js"

type CodeDef struct {
	underlying *js.Object
}

type CodePropsDef struct {
	*BasicHTMLElement
}

func CodeProps(f func(p *CodePropsDef)) *CodePropsDef {
	res := &CodePropsDef{
		BasicHTMLElement: newBasicHTMLElement(),
	}
	f(res)
	return res
}

func (d *CodeDef) reactElement() {}

func Code(props *CodePropsDef, children ...Element) *CodeDef {
	args := []interface{}{"code", props}

	for _, v := range children {
		args = append(args, elementToReactObj(v))
	}

	underlying := react.Call("createElement", args...)

	return &CodeDef{underlying: underlying}
}
