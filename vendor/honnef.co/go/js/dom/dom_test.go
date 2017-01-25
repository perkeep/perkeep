package dom

var _ Node = &BasicNode{}
var _ HTMLElement = &BasicHTMLElement{}
var _ Element = &BasicElement{}
var _ Document = &document{}
var _ Window = &window{}
var _ HTMLDocument = &htmlDocument{}
