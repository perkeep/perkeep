package react

import "github.com/gopherjs/gopherjs/js"

type TextAreaDef struct {
	underlying *js.Object
}

type TextAreaPropsDef struct {
	*BasicHTMLElement

	Placeholder  string `js:"placeholder"`
	Value        string `js:"value"`
	DefaultValue string `js:"defaultValue"`
}

func TextAreaProps(f func(p *TextAreaPropsDef)) *TextAreaPropsDef {
	res := &TextAreaPropsDef{
		BasicHTMLElement: newBasicHTMLElement(),
	}
	f(res)
	return res
}

func (d *TextAreaDef) reactElement() {}

func TextArea(props *TextAreaPropsDef, children ...Element) *TextAreaDef {
	args := []interface{}{"textarea", props}

	for _, v := range children {
		args = append(args, elementToReactObj(v))
	}

	underlying := react.Call("createElement", args...)

	return &TextAreaDef{underlying: underlying}
}
