package react

import "github.com/gopherjs/gopherjs/js"

type H3Def struct {
	underlying *js.Object
}

type H3PropsDef struct {
	*BasicHTMLElement
}

func H3Props(f func(p *H3PropsDef)) *H3PropsDef {
	res := &H3PropsDef{
		BasicHTMLElement: newBasicHTMLElement(),
	}
	f(res)
	return res
}

func (d *H3Def) reactElement() {}

func H3(props *H3PropsDef, child Element) *H3Def {
	underlying := react.Call("createElement", "h3", props, elementToReactObj(child))

	return &H3Def{underlying: underlying}
}
