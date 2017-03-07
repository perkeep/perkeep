package react

import "github.com/gopherjs/gopherjs/js"

type H1Def struct {
	underlying *js.Object
}

type H1PropsDef struct {
	*BasicHTMLElement
}

func H1Props(f func(p *H1PropsDef)) *H1PropsDef {
	res := &H1PropsDef{
		BasicHTMLElement: newBasicHTMLElement(),
	}
	f(res)
	return res
}

func (d *H1Def) reactElement() {}

func H1(props *H1PropsDef, child Element) *H1Def {
	underlying := react.Call("createElement", "h1", props, elementToReactObj(child))

	return &H1Def{underlying: underlying}
}
