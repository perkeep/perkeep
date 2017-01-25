package react

import "github.com/gopherjs/gopherjs/js"

type ADef struct {
	underlying *js.Object
}

type APropsDef struct {
	*BasicHTMLElement

	Target string `js:"target"`
	Href   string `js:"href"`
}

func AProps(f func(p *APropsDef)) *APropsDef {
	res := &APropsDef{
		BasicHTMLElement: newBasicHTMLElement(),
	}
	f(res)
	return res
}

func (d *ADef) reactElement() {}

func A(props *APropsDef, children ...Element) *ADef {
	args := []interface{}{"a", props}

	for _, v := range children {
		args = append(args, elementToReactObj(v))
	}

	underlying := react.Call("createElement", args...)

	return &ADef{underlying: underlying}
}
