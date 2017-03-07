package react

import "github.com/gopherjs/gopherjs/js"

type HRDef struct {
	underlying *js.Object
}

type HRPropsDef struct {
	*BasicHTMLElement
}

func HRProps(f func(p *HRPropsDef)) *HRPropsDef {
	res := &HRPropsDef{
		BasicHTMLElement: newBasicHTMLElement(),
	}
	f(res)
	return res
}

func (d *HRDef) reactElement() {}

func HR(props *HRPropsDef) *HRDef {
	underlying := react.Call("createElement", "hr", props)

	return &HRDef{underlying: underlying}
}
