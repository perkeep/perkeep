package react

import "github.com/gopherjs/gopherjs/js"

type LiDef struct {
	underlying *js.Object
}

type LiPropsDef struct {
	*BasicHTMLElement

	Role string `js:"role"`
}

func LiProps(f func(p *LiPropsDef)) *LiPropsDef {
	res := &LiPropsDef{
		BasicHTMLElement: newBasicHTMLElement(),
	}
	f(res)
	return res
}

func (d *LiDef) reactElement() {}

func Li(props *LiPropsDef, children ...Element) *LiDef {
	args := []interface{}{"li", props}

	for _, v := range children {
		args = append(args, elementToReactObj(v))
	}

	underlying := react.Call("createElement", args...)

	return &LiDef{underlying: underlying}
}
