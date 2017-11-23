// Copyright (c) 2016 Paul Jolly <paul@myitcv.org.uk>, all rights reserved.
// Use of this document is governed by a license found in the LICENSE document.

/*

Package react is a set of GopherJS bindings for Facebook's React, a Javascript
library for building user interfaces.

For more information see https://github.com/myitcv/react/wiki

*/
package react // import "myitcv.io/react"

//go:generate reactGen

import (
	"fmt"
	"reflect"

	"honnef.co/go/js/dom"

	"github.com/gopherjs/gopherjs/js"
	"github.com/gopherjs/jsbuiltin"

	// imported for the side effect of bundling react
	// build tags control whether this actually includes
	// js files or not
	_ "myitcv.io/react/internal/bundle"
	"myitcv.io/react/internal/core"
)

const (
	reactInternalInstance              = "_reactInternalInstance"
	reactCompProps                     = "props"
	reactCompLastState                 = "__lastState"
	reactComponentBuilder              = "__componentBuilder"
	reactCompDisplayName               = "displayName"
	reactCompSetState                  = "setState"
	reactCompState                     = "state"
	reactCompGetInitialState           = "getInitialState"
	reactCompShouldComponentUpdate     = "shouldComponentUpdate"
	reactCompComponentDidMount         = "componentDidMount"
	reactCompComponentWillReceiveProps = "componentWillReceiveProps"
	reactCompComponentWillMount        = "componentWillMount"
	reactCompComponentWillUnmount      = "componentWillUnmount"
	reactCompRender                    = "render"

	reactCreateElement = "createElement"
	reactCreateClass   = "createClass"
	reactDOMRender     = "render"

	nestedChildren         = "_children"
	nestedProps            = "_props"
	nestedState            = "_state"
	nestedComponentWrapper = "__ComponentWrapper"
)

var react = js.Global.Get("React")
var reactDOM = js.Global.Get("ReactDOM")
var object = js.Global.Get("Object")

// ComponentDef is embedded in a type definition to indicate the type is a component
type ComponentDef struct {
	elem *js.Object
}

var compMap = make(map[reflect.Type]*js.Object)

// S is the React representation of a string
type S = core.S

type elementHolder = core.ElementHolder

type Element = core.Element

type Component interface {
	ShouldComponentUpdateIntf(nextProps Props, prevState, nextState State) bool
	Render() Element
}

type componentWithWillMount interface {
	Component
	ComponentWillMount()
}

type componentWithDidMount interface {
	Component
	ComponentDidMount()
}

type componentWithWillReceiveProps interface {
	Component
	ComponentWillReceivePropsIntf(i interface{})
}

type componentWithGetInitialState interface {
	Component
	GetInitialStateIntf() State
}

type componentWithWillUnmount interface {
	Component
	ComponentWillUnmount()
}

type Props interface {
	IsProps()
	EqualsIntf(v Props) bool
}

type State interface {
	IsState()
	EqualsIntf(v State) bool
}

func (c ComponentDef) Props() Props {
	return unwrapValue(c.instance().Get(reactCompProps).Get(nestedProps)).(Props)
}

func (c ComponentDef) Children() []Element {
	v := c.instance().Get(reactCompProps).Get(nestedChildren)

	if v == js.Undefined {
		return nil
	}

	return unwrapValue(v).([]Element)
}

func (c ComponentDef) instance() *js.Object {
	return c.elem.Get("_instance")
}

func (c ComponentDef) SetState(i State) {
	cur := c.State()

	if i.EqualsIntf(cur) {
		return
	}

	res := object.New()
	res.Set(nestedState, wrapValue(i))
	c.instance().Set(reactCompLastState, res)
	c.instance().Call(reactCompSetState, res)
}

func (c ComponentDef) State() State {
	ok, err := jsbuiltin.In(reactCompLastState, c.instance())
	if err != nil {
		// TODO better handle this case... does that function even need to
		// return an error?
		panic(err)
	}

	if !ok {
		s := c.instance().Get(reactCompState)
		c.instance().Set(reactCompLastState, s)
	}

	return unwrapValue(c.instance().Get(reactCompLastState).Get(nestedState)).(State)
}

type ComponentBuilder func(elem ComponentDef) Component

func CreateElement(buildCmp ComponentBuilder, newprops Props, children ...Element) Element {
	cmp := buildCmp(ComponentDef{})
	typ := reflect.TypeOf(cmp)

	comp, ok := compMap[typ]
	if !ok {
		comp = buildReactComponent(typ, buildCmp)
		compMap[typ] = comp
	}

	propsWrap := object.New()
	if newprops != nil {
		propsWrap.Set(nestedProps, wrapValue(newprops))
	}

	if children != nil {
		propsWrap.Set(nestedChildren, wrapValue(children))
	}

	args := []interface{}{comp, propsWrap}

	for _, v := range children {
		args = append(args, v)
	}

	return &elementHolder{
		Elem: react.Call(reactCreateElement, args...),
	}
}

func createElement(cmp string, props interface{}, children ...Element) Element {
	args := []interface{}{cmp, props}

	for _, v := range children {
		args = append(args, v)
	}

	return &elementHolder{
		Elem: react.Call("createElement", args...),
	}
}

func buildReactComponent(typ reflect.Type, builder ComponentBuilder) *js.Object {
	compDef := object.New()
	compDef.Set(reactCompDisplayName, fmt.Sprintf("%v(%v)", typ.Name(), typ.PkgPath()))
	compDef.Set(reactComponentBuilder, builder)

	compDef.Set(reactCompGetInitialState, js.MakeFunc(func(this *js.Object, arguments []*js.Object) interface{} {
		elem := this.Get(reactInternalInstance)
		cmp := builder(ComponentDef{elem: elem})

		if cmp, ok := cmp.(componentWithGetInitialState); ok {
			x := cmp.GetInitialStateIntf()
			if x == nil {
				return nil
			}
			res := object.New()
			res.Set(nestedState, wrapValue(x))
			return res
		}

		return nil
	}))

	compDef.Set(reactCompShouldComponentUpdate, js.MakeFunc(func(this *js.Object, arguments []*js.Object) interface{} {
		elem := this.Get(reactInternalInstance)
		cmp := builder(ComponentDef{elem: elem})

		var nextProps Props
		var prevState State
		var nextState State

		if arguments[0] != nil {
			if ok, err := jsbuiltin.In(nestedProps, arguments[0]); err == nil && ok {
				nextProps = unwrapValue(arguments[0].Get(nestedProps)).(Props)
			}
		}

		if arguments[1] != nil {
			if ok, err := jsbuiltin.In(nestedState, arguments[1]); err == nil && ok {
				nextState = unwrapValue(arguments[1].Get(nestedState)).(State)
			}
		}

		// here we _deliberately_ get React's version of the current state
		// as opposed to the last state value
		if this != nil {
			if s := this.Get(reactCompState); s != nil {
				if v := unwrapValue(s.Get(nestedState)); v != nil {
					prevState = v.(State)
				}
			}
		}

		return cmp.ShouldComponentUpdateIntf(nextProps, prevState, nextState)
	}))

	compDef.Set(reactCompComponentDidMount, js.MakeFunc(func(this *js.Object, arguments []*js.Object) interface{} {
		elem := this.Get(reactInternalInstance)
		cmp := builder(ComponentDef{elem: elem})

		if cmp, ok := cmp.(componentWithDidMount); ok {
			cmp.ComponentDidMount()
		}

		return nil
	}))

	compDef.Set(reactCompComponentWillReceiveProps, js.MakeFunc(func(this *js.Object, arguments []*js.Object) interface{} {
		elem := this.Get(reactInternalInstance)
		cmp := builder(ComponentDef{elem: elem})

		if cmp, ok := cmp.(componentWithWillReceiveProps); ok {
			ourProps := unwrapValue(arguments[0].Get(nestedProps))
			cmp.ComponentWillReceivePropsIntf(ourProps)
		}

		return nil
	}))

	compDef.Set(reactCompComponentWillUnmount, js.MakeFunc(func(this *js.Object, arguments []*js.Object) interface{} {
		elem := this.Get(reactInternalInstance)
		cmp := builder(ComponentDef{elem: elem})

		if cmp, ok := cmp.(componentWithWillUnmount); ok {
			cmp.ComponentWillUnmount()
		}

		return nil
	}))

	compDef.Set(reactCompComponentWillMount, js.MakeFunc(func(this *js.Object, arguments []*js.Object) interface{} {
		elem := this.Get(reactInternalInstance)
		cmp := builder(ComponentDef{elem: elem})

		// TODO we can make this more efficient by not doing the type check
		// within the function body; it is known at the time of setting
		// "componentWillMount" on the compDef
		if cmp, ok := cmp.(componentWithWillMount); ok {
			cmp.ComponentWillMount()
		}

		return nil
	}))

	compDef.Set(reactCompRender, js.MakeFunc(func(this *js.Object, arguments []*js.Object) interface{} {
		elem := this.Get(reactInternalInstance)
		cmp := builder(ComponentDef{elem: elem})

		renderRes := cmp.Render()

		return renderRes
	}))

	return react.Call(reactCreateClass, compDef)
}

func Render(el Element, container dom.Element) Element {
	v := reactDOM.Call(reactDOMRender, el, container)

	return &elementHolder{Elem: v}
}
