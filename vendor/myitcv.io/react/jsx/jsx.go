/*

Package jsx allows you to render blocks of HTML as myitcv.io/react elements.
It is a temporary runtime solution for what will become a compile-time
transpilation, much like JSX's relationship with Javascript.

For more information see https://github.com/myitcv/react/wiki

*/
package jsx

import (
	"fmt"
	"strings"

	"myitcv.io/react"

	"github.com/russross/blackfriday"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// each of the parse* functions does zero validation
// this is intentional because everything is expected to
// go via the generic parse function

// TODO code generate these parse functions

func parseP(n *html.Node) *react.PElem {
	var kids []react.Element

	// TODO attributes

	for c := n.FirstChild; c != nil; c = c.NextSibling {
		kids = append(kids, parse(c))
	}

	return react.P(nil, kids...)
}

func parseHr(n *html.Node) *react.HrElem {
	// TODO attributes

	return react.Hr(nil)
}

func parseBr(n *html.Node) *react.BrElem {
	// TODO attributes

	return react.Br(nil)
}

func parseH1(n *html.Node) *react.H1Elem {
	var kids []react.Element

	// TODO attributes

	for c := n.FirstChild; c != nil; c = c.NextSibling {
		kids = append(kids, parse(c))
	}

	return react.H1(nil, kids...)
}

func parseSpan(n *html.Node) *react.SpanElem {
	var kids []react.Element

	var vp *react.SpanProps

	if len(n.Attr) > 0 {
		vp = new(react.SpanProps)

		for _, a := range n.Attr {
			switch a.Key {
			case "classname":
				vp.ClassName = a.Val
			case "style":
				vp.Style = parseCSS(a.Val)
			default:
				panic(fmt.Errorf("don't know how to handle <span> attribute %q", a.Key))
			}
		}
	}

	for c := n.FirstChild; c != nil; c = c.NextSibling {
		kids = append(kids, parse(c))
	}

	return react.Span(vp, kids...)
}

func parseI(n *html.Node) *react.IElem {
	var kids []react.Element

	var vp *react.IProps

	if len(n.Attr) > 0 {
		vp = new(react.IProps)

		for _, a := range n.Attr {
			switch a.Key {
			case "id":
				vp.ID = a.Val
			case "classname":
				vp.ClassName = a.Val
			default:
				panic(fmt.Errorf("don't know how to handle <i> attribute %q", a.Key))
			}
		}
	}

	for c := n.FirstChild; c != nil; c = c.NextSibling {
		kids = append(kids, parse(c))
	}

	return react.I(vp, kids...)
}

func parseFooter(n *html.Node) *react.FooterElem {
	var kids []react.Element

	var vp *react.FooterProps

	if len(n.Attr) > 0 {
		vp = new(react.FooterProps)

		for _, a := range n.Attr {
			switch a.Key {
			case "id":
				vp.ID = a.Val
			case "classname":
				vp.ClassName = a.Val
			default:
				panic(fmt.Errorf("don't know how to handle <footer> attribute %q", a.Key))
			}
		}
	}

	for c := n.FirstChild; c != nil; c = c.NextSibling {
		kids = append(kids, parse(c))
	}

	return react.Footer(vp, kids...)
}

func parseDiv(n *html.Node) *react.DivElem {
	var kids []react.Element

	var vp *react.DivProps

	if len(n.Attr) > 0 {
		vp = new(react.DivProps)

		for _, a := range n.Attr {
			switch a.Key {
			case "id":
				vp.ID = a.Val
			case "classname":
				vp.ClassName = a.Val
			case "style":
				vp.Style = parseCSS(a.Val)
			default:
				panic(fmt.Errorf("don't know how to handle <div> attribute %q", a.Key))
			}
		}
	}

	for c := n.FirstChild; c != nil; c = c.NextSibling {
		kids = append(kids, parse(c))
	}

	return react.Div(vp, kids...)
}

func parseButton(n *html.Node) *react.ButtonElem {
	var kids []react.Element

	var vp *react.ButtonProps

	if len(n.Attr) > 0 {
		vp = new(react.ButtonProps)

		for _, a := range n.Attr {
			switch a.Key {
			case "id":
				vp.ID = a.Val
			case "classname":
				vp.ClassName = a.Val
			default:
				panic(fmt.Errorf("don't know how to handle <div> attribute %q", a.Key))
			}
		}
	}

	for c := n.FirstChild; c != nil; c = c.NextSibling {
		kids = append(kids, parse(c))
	}

	return react.Button(vp, kids...)
}

func parseCode(n *html.Node) *react.CodeElem {
	var kids []react.Element

	// TODO attributes

	for c := n.FirstChild; c != nil; c = c.NextSibling {
		kids = append(kids, parse(c))
	}

	return react.Code(nil, kids...)
}

func parseH3(n *html.Node) *react.H3Elem {
	var kids []react.Element

	// TODO attributes

	for c := n.FirstChild; c != nil; c = c.NextSibling {
		kids = append(kids, parse(c))
	}

	return react.H3(nil, kids...)
}

func parseImg(n *html.Node) *react.ImgElem {
	var kids []react.Element

	var vp *react.ImgProps

	if len(n.Attr) > 0 {
		vp = new(react.ImgProps)

		for _, a := range n.Attr {
			switch a.Key {
			case "src":
				vp.Src = a.Val
			case "style":
				vp.Style = parseCSS(a.Val)
			default:
				panic(fmt.Errorf("don't know how to handle <img> attribute %q", a.Key))
			}
		}
	}

	for c := n.FirstChild; c != nil; c = c.NextSibling {
		kids = append(kids, parse(c))
	}

	return react.Img(vp, kids...)
}

func parseA(n *html.Node) *react.AElem {
	var kids []react.Element

	var vp *react.AProps

	if len(n.Attr) > 0 {
		vp = new(react.AProps)

		for _, a := range n.Attr {
			switch a.Key {
			case "href":
				vp.Href = a.Val
			case "target":
				vp.Target = a.Val
			default:
				panic(fmt.Errorf("don't know how to handle <a> attribute %q", a.Key))
			}
		}
	}

	for c := n.FirstChild; c != nil; c = c.NextSibling {
		kids = append(kids, parse(c))
	}

	return react.A(vp, kids...)
}

// TODO replace with proper parser
func parseCSS(s string) *react.CSS {
	res := new(react.CSS)

	parts := strings.Split(s, ";")

	for _, p := range parts {
		kv := strings.Split(p, ":")
		if len(kv) != 2 {
			panic(fmt.Errorf("invalid key-val %q in %q", p, s))
		}

		k, v := kv[0], kv[1]

		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		v = strings.Trim(v, "\"")

		switch k {
		case "overflow-y":
			res.OverflowY = v
		case "margin-top":
			res.MarginTop = v
		case "font-size":
			res.FontSize = v
		case "font-style":
			res.FontStyle = v
		default:
			panic(fmt.Errorf("unknown CSS key %q in %q", k, s))
		}
	}

	return res
}

func parse(n *html.Node) react.Element {
	switch n.Type {
	case html.TextNode:
		return react.S(n.Data)
	case html.ElementNode:
		// we will fall out from here...
	default:
		panic(fmt.Errorf("cannot handle NodeType %v", n.Type))
	}

	switch n.Data {
	case "p":
		return parseP(n)
	case "h1":
		return parseH1(n)
	case "code":
		return parseCode(n)
	case "h3":
		return parseH3(n)
	case "img":
		return parseImg(n)
	case "a":
		return parseA(n)
	case "footer":
		return parseFooter(n)
	case "div":
		return parseDiv(n)
	case "span":
		return parseSpan(n)
	case "hr":
		return parseHr(n)
	case "br":
		return parseBr(n)
	case "button":
		return parseButton(n)
	case "i":
		return parseI(n)
	default:
		panic(fmt.Errorf("cannot handle Element %v", n.Data))
	}
}

var htmlCache = make(map[string][]react.Element)

// HTML is a runtime JSX-like parsereact. It parses the supplied HTML string into
// myitcv.io/react element values. It exists as a stop-gap runtime solution to
// full JSX-like support within the GopherJS compilereact. It should only be used
// where the argument is a compile-time constant string (TODO enforce this
// within reactVet). HTML will panic in case s cannot be parsed as a valid HTML
// fragment
//
func HTML(s string) []react.Element {
	s = strings.TrimSpace(s)

	if v, ok := htmlCache[s]; ok {
		return v
	}

	// a dummy div for parsing the fragment
	div := &html.Node{
		Type:     html.ElementNode,
		Data:     "div",
		DataAtom: atom.Div,
	}

	elems, err := html.ParseFragment(strings.NewReader(s), div)
	if err != nil {
		panic(fmt.Errorf("failed to parse HTML %q: %v", s, err))
	}

	res := make([]react.Element, len(elems))

	for i, v := range elems {
		res[i] = parse(v)
	}

	htmlCache[s] = res

	return res
}

// HTMLElem is a convenience wrapper around HTML where only a single root
// element is expected. HTMLElem will panic if more than one HTML element
// results
//
func HTMLElem(s string) react.Element {
	res := HTML(s)

	if v := len(res); v != 1 {
		panic(fmt.Errorf("expected single element result from %q; got %v", s, v))
	}

	return res[0]
}

// Markdown is a runtime JSX-like parser for markdown. It parses the supplied
// markdown string into an HTML string and then hands off to the HTML function.
// Like the HTML function, it exists as a stop-gap runtime solution to full
// JSX-like support within the GopherJS compilereact. It should only be used where
// the argument is a compile-time constant string (TODO enforce this within
// reactVet). Markdown will panic in case the markdown string s results in an
// invalid HTML string
//
func Markdown(s string) []react.Element {

	h := blackfriday.MarkdownCommon([]byte(s))

	return HTML(string(h))
}
