package react

import "github.com/gopherjs/gopherjs/js"

type PDef struct {
	underlying *js.Object
}

type PPropsDef struct {
	*BasicHTMLElement
}

func PProps(f func(p *PPropsDef)) *PPropsDef {
	res := &PPropsDef{
		BasicHTMLElement: newBasicHTMLElement(),
	}
	f(res)
	return res
}

func (d *PDef) reactElement() {}

func P(props *PPropsDef, children ...Element) *PDef {
	args := []interface{}{"p", props}

	for _, v := range children {
		args = append(args, elementToReactObj(v))
	}

	underlying := react.Call("createElement", args...)

	return &PDef{underlying: underlying}
}
