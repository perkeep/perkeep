package react

import "github.com/gopherjs/gopherjs/js"

type UlDef struct {
	underlying *js.Object
}

type UlPropsDef struct {
	*BasicHTMLElement
}

func UlProps(f func(p *UlPropsDef)) *UlPropsDef {
	res := &UlPropsDef{
		BasicHTMLElement: newBasicHTMLElement(),
	}
	f(res)
	return res
}

func (d *UlDef) reactElement() {}

func Ul(props *UlPropsDef, children ...*LiDef) *UlDef {
	args := []interface{}{"ul", props}

	for _, v := range children {
		args = append(args, elementToReactObj(v))
	}

	underlying := react.Call("createElement", args...)

	return &UlDef{underlying: underlying}
}
