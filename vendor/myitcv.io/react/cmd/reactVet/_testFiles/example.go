package main

import (
	"myitcv.io/react/jsx"
)

const (
	c = "hello world"
)

func main() {
	var s = "hello world"

	_ = jsx.HTML(c)
	_ = jsx.HTML("hello world")
	_ = jsx.HTML(s) // ERROR

	_ = jsx.HTMLElem(c)
	_ = jsx.HTMLElem("hello world")
	_ = jsx.HTMLElem(s) // ERROR

	_ = jsx.Markdown(c)
	_ = jsx.Markdown("hello world")
	_ = jsx.Markdown(s) // ERROR
}
