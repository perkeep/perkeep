package main

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
)

type coreGen struct {
	*gen

	buf *bytes.Buffer
}

func newCoreGen(g *gen) *coreGen {
	return &coreGen{
		gen: g,
		buf: bytes.NewBuffer(nil),
	}
}

func (c *coreGen) pf(format string, vals ...interface{}) {
	fmt.Fprintf(c.buf, format, vals...)
}

func (c *coreGen) pln(vals ...interface{}) {
	fmt.Fprintln(c.buf, vals...)
}

func (c *coreGen) pt(tmpl string, val interface{}) {
	// on the basis most templates are for convenience define inline
	// as raw string literals which start the ` on one line but then start
	// the template on the next (for readability) we strip the first leading
	// \n if one exists
	tmpl = strings.TrimPrefix(tmpl, "\n")

	t := template.New("tmp")

	_, err := t.Parse(tmpl)
	if err != nil {
		fatalf("unable to parse template: %v", err)
	}

	err = t.Execute(c.buf, val)
	if err != nil {
		fatalf("cannot execute template: %v", err)
	}
}
