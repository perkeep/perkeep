package core

import "github.com/gopherjs/gopherjs/js"

type S string

func (s S) reactElement() {}

type ElementHolder struct {
	Elem *js.Object
}

func (r *ElementHolder) reactElement() {}

type Element interface {
	reactElement()
}
