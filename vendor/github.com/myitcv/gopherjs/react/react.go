package react

import (
	"reflect"

	"honnef.co/go/js/dom"

	"github.com/gopherjs/gopherjs/js"
)

const (
	reactProps = "props"
	reactState = "state"

	nestedProps = "_props"
	nestedState = "_state"
)

var react = js.Global.Get("React")
var reactDom = js.Global.Get("ReactDOM")

type ComponentDef struct {
	state interface{}
	elem  *js.Object
	this  *js.Object
}

var compMap = make(map[reflect.Type]*js.Object)

type S string

func (s S) reactElement() {}

type Element interface {
	reactElement()
}

type generatesElement interface {
	element() *js.Object
}

type Component interface {
	Render() Element

	setThis(this *js.Object)
	setElem(elem *js.Object)
}

type ComponentWithWillMount interface {
	Component
	ComponentWillMount()
}

type ComponentWithDidMount interface {
	Component
	ComponentDidMount()
}

type ComponentWithWillReceiveProps interface {
	Component
	ComponentWillReceivePropsIntf(i interface{})
}

type ComponentWithGetInitialState interface {
	Component
	GetInitialStateIntf() State
}

type ComponentWithWillUnmount interface {
	Component
	ComponentWillUnmount()
}

type State interface {
	IsState()
}

func (c *ComponentDef) reactElement() {}

func (c *ComponentDef) element() *js.Object {
	return c.elem
}

func (c *ComponentDef) Props() interface{} {
	if c.this != nil {
		return c.this.Get(reactProps).Get(nestedProps).Interface()
	}

	return c.elem.Get(reactProps).Get(nestedProps).Interface()
}

func (c *ComponentDef) SetState(i interface{}) {
	if c.state != i {
		res := js.Global.Get("Object").New()
		res.Set(nestedState, js.MakeWrapper(i))
		c.this.Call("setState", res)
	}
}

func (c *ComponentDef) setThis(this *js.Object) {
	c.this = this
}

func (c *ComponentDef) setElem(elem *js.Object) {
	c.elem = elem
}

func (c *ComponentDef) State() interface{} {
	return c.this.Get(reactState).Get(nestedState).Interface()
}

func BlessElement(cmp Component, newprops interface{}, children ...Element) {
	typ := reflect.TypeOf(cmp)

	comp, ok := compMap[typ]
	if !ok {
		comp = buildReactComponent(typ)
		compMap[typ] = comp
	}

	propsWrap := js.Global.Get("Object").New()
	if newprops != nil {
		propsWrap.Set(nestedProps, js.MakeWrapper(newprops))
	}
	propsWrap.Set("__ComponentWrapper", js.MakeWrapper(cmp))

	args := []interface{}{comp, propsWrap}

	for _, v := range children {
		args = append(args, elementToReactObj(v))
	}

	elem := react.Call("createElement", args...)

	cmp.setElem(elem)
}

func buildReactComponent(typ reflect.Type) *js.Object {
	compDef := js.Global.Get("Object").New()
	compDef.Set("displayName", typ.String())

	compDef.Set("getInitialState", js.MakeFunc(func(this *js.Object, arguments []*js.Object) interface{} {

		props := this.Get("props")
		cw := props.Get("__ComponentWrapper")

		if cmp, ok := cw.Interface().(ComponentWithGetInitialState); ok {
			x := cmp.GetInitialStateIntf()
			if x == nil {
				return nil
			}
			res := js.Global.Get("Object").New()
			res.Set(nestedState, js.MakeWrapper(x))
			return res
		}

		return nil
	}))

	compDef.Set("shouldComponentUpdate", js.MakeFunc(func(this *js.Object, arguments []*js.Object) interface{} {
		// props := this.Get("props")
		// cw := props.Get("__ComponentWrapper")

		// nextProps := arguments[0].Get("_props").Interface()
		// nextState := arguments[1].Get("state").Interface()

		// currProps := this.Get("props").Get("_props").Interface()
		// currState := this.Get("state").Get("state").Interface()

		// TODO support for custom shouldComponentUpdate will be placed here

		return true
	}))

	compDef.Set("componentDidMount", js.MakeFunc(func(this *js.Object, arguments []*js.Object) interface{} {
		props := this.Get("props")
		cw := props.Get("__ComponentWrapper")

		if cmp, ok := cw.Interface().(ComponentWithDidMount); ok {
			cmp.ComponentDidMount()
		}

		return nil
	}))

	compDef.Set("componentWillReceiveProps", js.MakeFunc(func(this *js.Object, arguments []*js.Object) interface{} {
		props := this.Get("props")
		cw := props.Get("__ComponentWrapper")

		if cmp, ok := cw.Interface().(ComponentWithWillReceiveProps); ok {
			ourProps := arguments[0].Get(nestedProps).Interface()
			cmp.ComponentWillReceivePropsIntf(ourProps)
		}

		return nil
	}))

	compDef.Set("componentWillUnmount", js.MakeFunc(func(this *js.Object, arguments []*js.Object) interface{} {
		props := this.Get("props")
		cw := props.Get("__ComponentWrapper")

		if cmp, ok := cw.Interface().(ComponentWithWillUnmount); ok {
			cmp.ComponentWillUnmount()
		}

		return nil
	}))

	compDef.Set("componentWillMount", js.MakeFunc(func(this *js.Object, arguments []*js.Object) interface{} {
		props := this.Get("props")
		cw := props.Get("__ComponentWrapper")
		cmp := cw.Interface().(Component)

		cmp.setThis(this)

		// TODO we can make this more efficient by not doing the type check
		// within the function body; it is known at the time of setting
		// "componentWillMount" on the compDef
		if cmp, ok := cmp.(ComponentWithWillMount); ok {
			cmp.ComponentWillMount()
		}

		return nil
	}))

	compDef.Set("render", js.MakeFunc(func(this *js.Object, arguments []*js.Object) interface{} {
		props := this.Get("props")
		cw := props.Get("__ComponentWrapper")
		cmp := cw.Interface().(Component)

		renderRes := cmp.Render()

		return elementToReactObj(renderRes)
	}))

	return react.Call("createClass", compDef)
}

func elementToReactObj(el Element) interface{} {
	if el, ok := el.(generatesElement); ok {
		return el.element()
	}

	return el
}

func Render(el Element, container dom.Element) {
	reactDom.Call("render", elementToReactObj(el), container)
}
