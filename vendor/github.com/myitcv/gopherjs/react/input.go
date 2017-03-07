package react

import "github.com/gopherjs/gopherjs/js"

type InputDef struct {
	underlying *js.Object
}

type InputPropsDef struct {
	*BasicHTMLElement

	Placeholder  string `js:"placeholder"`
	Type         string `js:"type"`
	Value        string `js:"value"`
	DefaultValue string `js:"defaultValue"`
}

func InputProps(f func(p *InputPropsDef)) *InputPropsDef {
	res := &InputPropsDef{
		BasicHTMLElement: newBasicHTMLElement(),
	}
	f(res)
	return res
}

func (d *InputDef) reactElement() {}

func Input(props *InputPropsDef) *InputDef {
	args := []interface{}{"input", props}

	underlying := react.Call("createElement", args...)

	return &InputDef{
		underlying: underlying,
	}
}
