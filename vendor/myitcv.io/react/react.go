// Copyright (c) 2016 Paul Jolly <paul@myitcv.org.uk>, all rights reserved.
// Use of this document is governed by a license found in the LICENSE document.

/*

Package react is a set of GopherJS bindings for Facebook's React, a Javascript
library for building user interfaces.

For more information see https://github.com/myitcv/react/wiki

*/
package react // import "myitcv.io/react"

//go:generate cssGen
//go:generate coreGen

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
	reactCompProps                     = "props"
	reactCompLastState                 = "__lastState"
	reactComponentBuilder              = "__componentBuilder"
	reactCompDisplayName               = "displayName"
	reactCompSetState                  = "setState"
	reactCompForceUpdate               = "forceUpdate"
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
var symbolFragment = react.Get("Fragment")

// ComponentDef is embedded in a type definition to indicate the type is a component
type ComponentDef struct {
	elem *js.Object
}

var compMap = make(map[reflect.Type]*js.Object)

// S is the React representation of a string
type S = core.S

func Sprintf(format string, args ...interface{}) S {
	return S(fmt.Sprintf(format, args...))
}

type elementHolder = core.ElementHolder

type Element = core.Element

type Component interface {
	RendersElement() Element
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
	return *(unwrapValue(c.elem.Get(reactCompProps).Get(nestedProps)).(*Props))
}

func (c ComponentDef) Children() []Element {
	v := c.elem.Get(reactCompProps).Get(nestedChildren)

	if v == js.Undefined {
		return nil
	}

	return *(unwrapValue(v).(*[]Element))
}

func (c ComponentDef) SetState(i State) {
	rs := c.elem.Get(reactCompState)
	is := rs.Get(nestedState)

	cur := *(unwrapValue(is.Get(reactCompLastState)).(*State))

	if i.EqualsIntf(cur) {
		return
	}

	is.Set(reactCompLastState, wrapValue(&i))
	c.elem.Call(reactCompForceUpdate)
}

func (c ComponentDef) State() State {
	rs := c.elem.Get(reactCompState)
	is := rs.Get(nestedState)

	cur := *(unwrapValue(is.Get(reactCompLastState)).(*State))

	return cur
}

func (c ComponentDef) ForceUpdate() {
	c.elem.Call(reactCompForceUpdate)
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
		propsWrap.Set(nestedProps, wrapValue(&newprops))
	}

	if children != nil {
		propsWrap.Set(nestedChildren, wrapValue(&children))
	}

	args := []interface{}{comp, propsWrap}

	for _, v := range children {
		args = append(args, v)
	}

	return &elementHolder{
		Elem: react.Call(reactCreateElement, args...),
	}
}

func createElement(cmp interface{}, props interface{}, children ...Element) Element {
	args := []interface{}{cmp, props}

	for _, v := range children {
		args = append(args, v)
	}

	return &elementHolder{
		Elem: react.Call(reactCreateElement, args...),
	}
}

func buildReactComponent(typ reflect.Type, builder ComponentBuilder) *js.Object {
	compDef := object.New()
	compDef.Set(reactCompDisplayName, fmt.Sprintf("%v(%v)", typ.Name(), typ.PkgPath()))
	compDef.Set(reactComponentBuilder, builder)

	compDef.Set(reactCompGetInitialState, js.MakeFunc(func(this *js.Object, arguments []*js.Object) interface{} {
		elem := this
		cmp := builder(ComponentDef{elem: elem})

		var wv *js.Object

		res := object.New()
		is := object.New()

		if cmp, ok := cmp.(componentWithGetInitialState); ok {
			x := cmp.GetInitialStateIntf()
			wv = wrapValue(&x)
		}

		res.Set(nestedState, is)
		is.Set(reactCompLastState, wv)

		return res
	}))

	compDef.Set(reactCompShouldComponentUpdate, js.MakeFunc(func(this *js.Object, arguments []*js.Object) interface{} {
		var nextProps Props
		var curProps Props

		// whether a component should update is only a function of its props
		// ... and a component does not need to have props
		//
		// the only way we have of determining that here is whether the this
		// object has a props property that has a non-nil nestedProps property

		if this != nil {
			if p := this.Get(reactCompProps); p != nil {
				if ok, err := jsbuiltin.In(nestedProps, p); err == nil && ok {
					if v := (p.Get(nestedProps)); v != nil {
						curProps = *(unwrapValue(v).(*Props))
					}
				} else {
					return false
				}
			}
		}

		if arguments[0] != nil {
			if ok, err := jsbuiltin.In(nestedProps, arguments[0]); err == nil && ok {
				nextProps = *(unwrapValue(arguments[0].Get(nestedProps)).(*Props))
			}
		}

		return !curProps.EqualsIntf(nextProps)
	}))

	compDef.Set(reactCompComponentDidMount, js.MakeFunc(func(this *js.Object, arguments []*js.Object) interface{} {
		elem := this
		cmp := builder(ComponentDef{elem: elem})

		if cmp, ok := cmp.(componentWithDidMount); ok {
			cmp.ComponentDidMount()
		}

		return nil
	}))

	compDef.Set(reactCompComponentWillReceiveProps, js.MakeFunc(func(this *js.Object, arguments []*js.Object) interface{} {
		elem := this
		cmp := builder(ComponentDef{elem: elem})

		if cmp, ok := cmp.(componentWithWillReceiveProps); ok {
			ourProps := *(unwrapValue(arguments[0].Get(nestedProps)).(*Props))
			cmp.ComponentWillReceivePropsIntf(ourProps)
		}

		return nil
	}))

	compDef.Set(reactCompComponentWillUnmount, js.MakeFunc(func(this *js.Object, arguments []*js.Object) interface{} {
		elem := this
		cmp := builder(ComponentDef{elem: elem})

		if cmp, ok := cmp.(componentWithWillUnmount); ok {
			cmp.ComponentWillUnmount()
		}

		return nil
	}))

	compDef.Set(reactCompComponentWillMount, js.MakeFunc(func(this *js.Object, arguments []*js.Object) interface{} {
		elem := this
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
		elem := this
		cmp := builder(ComponentDef{elem: elem})

		renderRes := cmp.RendersElement()

		return renderRes
	}))

	return react.Call(reactCreateClass, compDef)
}

func Render(el Element, container dom.Element) Element {
	v := reactDOM.Call(reactDOMRender, el, container)

	return &elementHolder{Elem: v}
}
