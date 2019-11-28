package jquery

import "github.com/gopherjs/gopherjs/js"

const (
	JQ = "jQuery"
	//keys
	BLUR     = "blur"
	CHANGE   = "change"
	CLICK    = "click"
	DBLCLICK = "dblclick"
	FOCUS    = "focus"
	FOCUSIN  = "focusin"
	FOCUSOUT = "focusout"
	HOVER    = "hover"
	KEYDOWN  = "keydown"
	KEYPRESS = "keypress"
	KEYUP    = "keyup"
	//form
	SUBMIT = "submit"
	LOAD   = "load"
	UNLOAD = "unload"
	RESIZE = "resize"
	//mouse
	MOUSEDOWN  = "mousedown"
	MOUSEENTER = "mouseenter"
	MOUSELEAVE = "mouseleave"
	MOUSEMOVE  = "mousemove"
	MOUSEOUT   = "mouseout"
	MOUSEOVER  = "mouseover"
	MOUSEUP    = "mouseup"
	//touch
	TOUCHSTART  = "touchstart"
	TOUCHMOVE   = "touchmove"
	TOUCHEND    = "touchend"
	TOUCHENTER  = "touchenter"
	TOUCHLEAVE  = "touchleave"
	TOUCHCANCEL = "touchcancel"
	//ajax Events
	AJAXSTART    = "ajaxStart"
	BEFORESEND   = "beforeSend"
	AJAXSEND     = "ajaxSend"
	SUCCESS      = "success"
	AJAXSUCESS   = "ajaxSuccess"
	ERROR        = "error"
	AJAXERROR    = "ajaxError"
	COMPLETE     = "complete"
	AJAXCOMPLETE = "ajaxComplete"
	AJAXSTOP     = "ajaxStop"
)

type JQuery struct {
	o        *js.Object
	Jquery   string `js:"jquery"`
	Selector string `js:"selector"` //deprecated according jquery docs
	Length   int    `js:"length"`
	Context  string `js:"context"`
}

type Event struct {
	*js.Object
	KeyCode        int        `js:"keyCode"`
	Target         *js.Object `js:"target"`
	CurrentTarget  *js.Object `js:"currentTarget"`
	DelegateTarget *js.Object `js:"delegateTarget"`
	RelatedTarget  *js.Object `js:"relatedTarget"`
	Data           *js.Object `js:"data"`
	Result         *js.Object `js:"result"`
	Which          int        `js:"which"`
	Namespace      string     `js:"namespace"`
	MetaKey        bool       `js:"metaKey"`
	ShiftKey       bool       `js:"shiftKey"`
	CtrlKey        bool       `js:"ctrlKey"`
	PageX          int        `js:"pageX"`
	PageY          int        `js:"pageY"`
	Type           string     `js:"type"`
}

type JQueryCoordinates struct {
	Left int
	Top  int
}

func (event *Event) PreventDefault() {
	event.Call("preventDefault")
}

func (event *Event) IsDefaultPrevented() bool {
	return event.Call("isDefaultPrevented").Bool()
}

func (event *Event) IsImmediatePropogationStopped() bool {
	return event.Call("isImmediatePropogationStopped").Bool()
}

func (event *Event) IsPropagationStopped() bool {
	return event.Call("isPropagationStopped").Bool()
}

func (event *Event) StopImmediatePropagation() {
	event.Call("stopImmediatePropagation")
}

func (event *Event) StopPropagation() {
	event.Call("stopPropagation")
}

func log(i ...interface{}) {
	js.Global.Get("console").Call("log", i...)
}

//JQuery constructor
func NewJQuery(args ...interface{}) JQuery {
	return JQuery{o: js.Global.Get(JQ).New(args...)}
}

//static function
func Trim(text string) string {
	return js.Global.Get(JQ).Call("trim", text).String()
}

//static function
func GlobalEval(cmd string) {
	js.Global.Get(JQ).Call("globalEval", cmd)
}

//static function
func Type(sth interface{}) string {
	return js.Global.Get(JQ).Call("type", sth).String()
}

//static function
func IsPlainObject(sth interface{}) bool {
	return js.Global.Get(JQ).Call("isPlainObject", sth).Bool()
}

//static function
func IsEmptyObject(sth interface{}) bool {
	return js.Global.Get(JQ).Call("isEmptyObject", sth).Bool()
}

//static function
func IsFunction(sth interface{}) bool {
	return js.Global.Get(JQ).Call("isFunction", sth).Bool()
}

//static function
func IsNumeric(sth interface{}) bool {
	return js.Global.Get(JQ).Call("isNumeric", sth).Bool()
}

//static function
func IsXMLDoc(sth interface{}) bool {
	return js.Global.Get(JQ).Call("isXMLDoc", sth).Bool()
}

//static function
func IsWindow(sth interface{}) bool {
	return js.Global.Get(JQ).Call("isWindow", sth).Bool()
}

//static function
func InArray(val interface{}, arr []interface{}) int {
	return js.Global.Get(JQ).Call("inArray", val, arr).Int()
}

//static function
func Contains(container interface{}, contained interface{}) bool {
	return js.Global.Get(JQ).Call("contains", container, contained).Bool()
}

//static function
func ParseHTML(text string) []interface{} {
	return js.Global.Get(JQ).Call("parseHTML", text).Interface().([]interface{})
}

//static function
func ParseXML(text string) interface{} {
	return js.Global.Get(JQ).Call("parseXML", text).Interface()
}

//static function
func ParseJSON(text string) interface{} {
	return js.Global.Get(JQ).Call("parseJSON", text).Interface()
}

//static function
func Grep(arr []interface{}, fn func(interface{}, int) bool) []interface{} {
	return js.Global.Get(JQ).Call("grep", arr, fn).Interface().([]interface{})
}

//static function
func Noop() interface{} {
	return js.Global.Get(JQ).Get("noop").Interface()
}

//static function
func Now() float64 {
	return js.Global.Get(JQ).Call("now").Float()
}

//static function
func Unique(arr *js.Object) *js.Object {
	return js.Global.Get(JQ).Call("unique", arr)
}

//methods
func (j JQuery) Each(fn func(int, interface{})) JQuery {
	j.o = j.o.Call("each", fn)
	return j
}

func (j JQuery) Call(name string, args ...interface{}) JQuery {
	return NewJQuery(j.o.Call(name, args...))
}

func (j JQuery) Underlying() *js.Object {
	return j.o
}

func (j JQuery) Get(i ...interface{}) *js.Object {
	return j.o.Call("get", i...)
}

func (j JQuery) Append(i ...interface{}) JQuery {
	j.o = j.o.Call("append", i...)
	return j
}

func (j JQuery) Empty() JQuery {
	j.o = j.o.Call("empty")
	return j
}

func (j JQuery) Detach(i ...interface{}) JQuery {
	j.o = j.o.Call("detach", i...)
	return j
}

func (j JQuery) Eq(idx int) JQuery {
	j.o = j.o.Call("eq", idx)
	return j
}
func (j JQuery) FadeIn(i ...interface{}) JQuery {
	j.o = j.o.Call("fadeIn", i...)
	return j
}

func (j JQuery) Delay(i ...interface{}) JQuery {
	j.o = j.o.Call("delay", i...)
	return j
}

func (j JQuery) ToArray() []interface{} {
	return j.o.Call("toArray").Interface().([]interface{})
}

func (j JQuery) Remove(i ...interface{}) JQuery {
	j.o = j.o.Call("remove", i...)
	return j
}

func (j JQuery) Stop(i ...interface{}) JQuery {
	j.o = j.o.Call("stop", i...)
	return j
}

func (j JQuery) AddBack(i ...interface{}) JQuery {
	j.o = j.o.Call("addBack", i...)
	return j
}

func (j JQuery) Css(name string) string {
	return j.o.Call("css", name).String()
}

func (j JQuery) CssArray(arr ...string) map[string]interface{} {
	return j.o.Call("css", arr).Interface().(map[string]interface{})
}

func (j JQuery) SetCss(i ...interface{}) JQuery {
	j.o = j.o.Call("css", i...)
	return j
}

func (j JQuery) Text() string {
	return j.o.Call("text").String()
}

func (j JQuery) SetText(i interface{}) JQuery {

	switch i.(type) {
	case func(int, string) string, string:
	default:
		print("SetText Argument should be 'string' or 'func(int, string) string'")
	}
	j.o = j.o.Call("text", i)
	return j
}

func (j JQuery) Val() string {
	return j.o.Call("val").String()
}

func (j JQuery) SetVal(i interface{}) JQuery {
	j.o.Call("val", i)
	return j
}

//can return string or bool
func (j JQuery) Prop(property string) interface{} {
	return j.o.Call("prop", property).Interface()
}

func (j JQuery) SetProp(i ...interface{}) JQuery {
	j.o = j.o.Call("prop", i...)
	return j
}

func (j JQuery) RemoveProp(property string) JQuery {
	j.o = j.o.Call("removeProp", property)
	return j
}

func (j JQuery) Attr(property string) string {
	attr := j.o.Call("attr", property)
	if attr == js.Undefined {
		return ""
	}
	return attr.String()
}

func (j JQuery) SetAttr(i ...interface{}) JQuery {
	j.o = j.o.Call("attr", i...)
	return j
}

func (j JQuery) RemoveAttr(property string) JQuery {
	j.o = j.o.Call("removeAttr", property)
	return j
}

func (j JQuery) HasClass(class string) bool {
	return j.o.Call("hasClass", class).Bool()
}

func (j JQuery) AddClass(i interface{}) JQuery {
	switch i.(type) {
	case func(int, string) string, string:
	default:
		print("addClass Argument should be 'string' or 'func(int, string) string'")
	}
	j.o = j.o.Call("addClass", i)
	return j
}

func (j JQuery) RemoveClass(property string) JQuery {
	j.o = j.o.Call("removeClass", property)
	return j
}

func (j JQuery) ToggleClass(i ...interface{}) JQuery {
	j.o = j.o.Call("toggleClass", i...)
	return j
}

func (j JQuery) Focus() JQuery {
	j.o = j.o.Call("focus")
	return j
}

func (j JQuery) Blur() JQuery {
	j.o = j.o.Call("blur")
	return j
}

func (j JQuery) ReplaceAll(i interface{}) JQuery {
	j.o = j.o.Call("replaceAll", i)
	return j

}
func (j JQuery) ReplaceWith(i interface{}) JQuery {
	j.o = j.o.Call("replaceWith", i)
	return j
}

func (j JQuery) After(i ...interface{}) JQuery {
	j.o = j.o.Call("after", i)
	return j

}

func (j JQuery) Before(i ...interface{}) JQuery {
	j.o = j.o.Call("before", i...)
	return j
}

func (j JQuery) Prepend(i ...interface{}) JQuery {
	j.o = j.o.Call("prepend", i...)
	return j
}

func (j JQuery) PrependTo(i interface{}) JQuery {
	j.o = j.o.Call("prependTo", i)
	return j
}

func (j JQuery) AppendTo(i interface{}) JQuery {
	j.o = j.o.Call("appendTo", i)
	return j
}

func (j JQuery) InsertAfter(i interface{}) JQuery {
	j.o = j.o.Call("insertAfter", i)
	return j

}

func (j JQuery) InsertBefore(i interface{}) JQuery {
	j.o = j.o.Call("insertBefore", i)
	return j
}

func (j JQuery) Show(i ...interface{}) JQuery {
	j.o = j.o.Call("show", i...)
	return j
}

func (j JQuery) Hide(i ...interface{}) JQuery {
	j.o.Call("hide", i...)
	return j
}

func (j JQuery) Toggle(i ...interface{}) JQuery {
	j.o = j.o.Call("toggle", i...)
	return j
}

func (j JQuery) Contents() JQuery {
	j.o = j.o.Call("contents")
	return j
}

func (j JQuery) Html() string {
	return j.o.Call("html").String()
}

func (j JQuery) SetHtml(i interface{}) JQuery {

	switch i.(type) {
	case func(int, string) string, string:
	default:
		print("SetHtml Argument should be 'string' or 'func(int, string) string'")
	}

	j.o = j.o.Call("html", i)
	return j
}

func (j JQuery) Closest(i ...interface{}) JQuery {
	j.o = j.o.Call("closest", i...)
	return j
}

func (j JQuery) End() JQuery {
	j.o = j.o.Call("end")
	return j
}

func (j JQuery) Add(i ...interface{}) JQuery {
	j.o = j.o.Call("add", i...)
	return j
}

func (j JQuery) Clone(b ...interface{}) JQuery {
	j.o = j.o.Call("clone", b...)
	return j
}

func (j JQuery) Height() int {
	return j.o.Call("height").Int()
}

func (j JQuery) SetHeight(value string) JQuery {
	j.o = j.o.Call("height", value)
	return j
}

func (j JQuery) Width() int {
	return j.o.Call("width").Int()
}

func (j JQuery) SetWidth(i interface{}) JQuery {

	switch i.(type) {
	case func(int, string) string, string:
	default:
		print("SetWidth Argument should be 'string' or 'func(int, string) string'")
	}

	j.o = j.o.Call("width", i)
	return j
}

func (j JQuery) Index(i interface{}) int {
	return j.o.Call("index", i).Int()
}

func (j JQuery) InnerHeight() int {
	return j.o.Call("innerHeight").Int()
}

func (j JQuery) InnerWidth() int {
	return j.o.Call("innerWidth").Int()
}

func (j JQuery) Offset() JQueryCoordinates {
	obj := j.o.Call("offset")
	return JQueryCoordinates{Left: obj.Get("left").Int(), Top: obj.Get("top").Int()}
}

func (j JQuery) SetOffset(jc JQueryCoordinates) JQuery {
	j.o = j.o.Call("offset", jc)
	return j
}

func (j JQuery) OuterHeight(includeMargin ...bool) int {
	if len(includeMargin) == 0 {
		return j.o.Call("outerHeight").Int()
	}
	return j.o.Call("outerHeight", includeMargin[0]).Int()
}
func (j JQuery) OuterWidth(includeMargin ...bool) int {

	if len(includeMargin) == 0 {
		return j.o.Call("outerWidth").Int()
	}
	return j.o.Call("outerWidth", includeMargin[0]).Int()
}

func (j JQuery) Position() JQueryCoordinates {
	obj := j.o.Call("position")
	return JQueryCoordinates{Left: obj.Get("left").Int(), Top: obj.Get("top").Int()}
}

func (j JQuery) ScrollLeft() int {
	return j.o.Call("scrollLeft").Int()
}
func (j JQuery) SetScrollLeft(value int) JQuery {
	j.o = j.o.Call("scrollLeft", value)
	return j
}

func (j JQuery) ScrollTop() int {
	return j.o.Call("scrollTop").Int()
}
func (j JQuery) SetScrollTop(value int) JQuery {
	j.o = j.o.Call("scrollTop", value)
	return j
}

func (j JQuery) ClearQueue(queueName string) JQuery {
	j.o = j.o.Call("clearQueue", queueName)
	return j
}

func (j JQuery) SetData(key string, value interface{}) JQuery {
	j.o = j.o.Call("data", key, value)
	return j
}

func (j JQuery) Data(key string) interface{} {
	result := j.o.Call("data", key)
	if result == js.Undefined {
		return nil
	}
	return result.Interface()
}

func (j JQuery) Dequeue(queueName string) JQuery {
	j.o = j.o.Call("dequeue", queueName)
	return j
}

func (j JQuery) RemoveData(name string) JQuery {
	j.o = j.o.Call("removeData", name)
	return j
}

func (j JQuery) OffsetParent() JQuery {
	j.o = j.o.Call("offsetParent")
	return j
}

func (j JQuery) Parent(i ...interface{}) JQuery {
	j.o = j.o.Call("parent", i...)
	return j
}

func (j JQuery) Parents(i ...interface{}) JQuery {
	j.o = j.o.Call("parents", i...)
	return j
}

func (j JQuery) ParentsUntil(i ...interface{}) JQuery {
	j.o = j.o.Call("parentsUntil", i...)
	return j
}

func (j JQuery) Prev(i ...interface{}) JQuery {
	j.o = j.o.Call("prev", i...)
	return j
}

func (j JQuery) PrevAll(i ...interface{}) JQuery {
	j.o = j.o.Call("prevAll", i...)
	return j
}

func (j JQuery) PrevUntil(i ...interface{}) JQuery {
	j.o = j.o.Call("prevUntil", i...)
	return j
}

func (j JQuery) Siblings(i ...interface{}) JQuery {
	j.o = j.o.Call("siblings", i...)
	return j
}

func (j JQuery) Slice(i ...interface{}) JQuery {
	j.o = j.o.Call("slice", i...)
	return j
}

func (j JQuery) Children(selector interface{}) JQuery {
	j.o = j.o.Call("children", selector)
	return j
}

func (j JQuery) Unwrap() JQuery {
	j.o = j.o.Call("unwrap")
	return j
}

func (j JQuery) Wrap(obj interface{}) JQuery {
	j.o = j.o.Call("wrap", obj)
	return j
}

func (j JQuery) WrapAll(i interface{}) JQuery {
	j.o = j.o.Call("wrapAll", i)
	return j
}

func (j JQuery) WrapInner(i interface{}) JQuery {
	j.o = j.o.Call("wrapInner", i)
	return j
}

func (j JQuery) Next(i ...interface{}) JQuery {
	j.o = j.o.Call("next", i...)
	return j
}

func (j JQuery) NextAll(i ...interface{}) JQuery {
	j.o = j.o.Call("nextAll", i...)
	return j
}

func (j JQuery) NextUntil(i ...interface{}) JQuery {
	j.o = j.o.Call("nextUntil", i...)
	return j
}

func (j JQuery) Not(i ...interface{}) JQuery {
	j.o = j.o.Call("not", i...)
	return j
}

func (j JQuery) Filter(i ...interface{}) JQuery {
	j.o = j.o.Call("filter", i...)
	return j
}

func (j JQuery) Find(i ...interface{}) JQuery {
	j.o = j.o.Call("find", i...)
	return j
}

func (j JQuery) First() JQuery {
	j.o = j.o.Call("first")
	return j
}

func (j JQuery) Has(selector string) JQuery {
	j.o = j.o.Call("has", selector)
	return j
}

func (j JQuery) Is(i ...interface{}) bool {
	return j.o.Call("is", i...).Bool()
}

func (j JQuery) Last() JQuery {
	j.o = j.o.Call("last")
	return j
}

func (j JQuery) Ready(handler func()) JQuery {
	j.o = j.o.Call("ready", handler)
	return j
}

func (j JQuery) Resize(i ...interface{}) JQuery {
	j.o = j.o.Call("resize", i...)
	return j
}

func (j JQuery) Scroll(i ...interface{}) JQuery {
	j.o = j.o.Call("scroll", i...)
	return j
}

func (j JQuery) FadeOut(i ...interface{}) JQuery {
	j.o = j.o.Call("fadeOut", i...)
	return j
}
func (j JQuery) FadeToggle(i ...interface{}) JQuery {
	j.o = j.o.Call("fadeToggle", i...)
	return j
}

func (j JQuery) SlideDown(i ...interface{}) JQuery {
	j.o = j.o.Call("slideDown", i...)
	return j
}
func (j JQuery) SlideToggle(i ...interface{}) JQuery {
	j.o = j.o.Call("slideToggle", i...)
	return j
}
func (j JQuery) SlideUp(i ...interface{}) JQuery {
	j.o = j.o.Call("slideUp", i...)
	return j
}

func (j JQuery) Select(i ...interface{}) JQuery {
	j.o = j.o.Call("select", i...)
	return j
}

func (j JQuery) Submit(i ...interface{}) JQuery {
	j.o = j.o.Call("submit", i...)
	return j
}

func (j JQuery) Trigger(i ...interface{}) JQuery {
	j.o = j.o.Call("trigger", i...)
	return j
}

func (j JQuery) On(i ...interface{}) JQuery {
	j.o = j.o.Call("on", i...)
	return j
}

func (j JQuery) One(i ...interface{}) JQuery {
	j.o = j.o.Call("one", i...)
	return j
}

func (j JQuery) Off(i ...interface{}) JQuery {
	j.o = j.o.Call("off", i...)
	return j
}

//ajax
func Param(params map[string]interface{}) {
	js.Global.Get(JQ).Call("param", params)
}

func (j JQuery) Load(i ...interface{}) JQuery {
	j.o = j.o.Call("load", i...)
	return j
}

func (j JQuery) Serialize() string {
	return j.o.Call("serialize").String()
}

func (j JQuery) SerializeArray() *js.Object {
	return j.o.Call("serializeArray")
}

func Ajax(options map[string]interface{}) Deferred {
	return Deferred{js.Global.Get(JQ).Call("ajax", options)}
}

func AjaxPrefilter(i ...interface{}) {
	js.Global.Get(JQ).Call("ajaxPrefilter", i...)
}

func AjaxSetup(options map[string]interface{}) {
	js.Global.Get(JQ).Call("ajaxSetup", options)
}

func AjaxTransport(i ...interface{}) {
	js.Global.Get(JQ).Call("ajaxTransport", i...)
}

func Get(i ...interface{}) Deferred {
	return Deferred{js.Global.Get(JQ).Call("get", i...)}
}

func Post(i ...interface{}) Deferred {
	return Deferred{js.Global.Get(JQ).Call("post", i...)}
}

func GetJSON(i ...interface{}) Deferred {
	return Deferred{js.Global.Get(JQ).Call("getJSON", i...)}
}

func GetScript(i ...interface{}) Deferred {
	return Deferred{js.Global.Get(JQ).Call("getScript", i...)}
}

func (d Deferred) Promise() *js.Object {
	return d.Call("promise")
}

type Deferred struct {
	*js.Object
}

func (d Deferred) Then(fn ...interface{}) Deferred {
	return Deferred{d.Call("then", fn...)}
}

func (d Deferred) Always(fn ...interface{}) Deferred {
	return Deferred{d.Call("always", fn...)}
}

func (d Deferred) Done(fn ...interface{}) Deferred {
	return Deferred{d.Call("done", fn...)}
}

func (d Deferred) Fail(fn ...interface{}) Deferred {
	return Deferred{d.Call("fail", fn...)}
}

func (d Deferred) Progress(fn interface{}) Deferred {
	return Deferred{d.Call("progress", fn)}

}

func When(d ...interface{}) Deferred {
	return Deferred{js.Global.Get(JQ).Call("when", d...)}
}

func (d Deferred) State() string {
	return d.Call("state").String()
}

func NewDeferred() Deferred {
	return Deferred{js.Global.Get(JQ).Call("Deferred")}
}

func (d Deferred) Resolve(i ...interface{}) Deferred {
	return Deferred{d.Call("resolve", i...)}
}

func (d Deferred) Reject(i ...interface{}) Deferred {
	return Deferred{d.Call("reject", i...)}

}

func (d Deferred) Notify(i interface{}) Deferred {
	return Deferred{d.Call("notify", i)}

}

//2do: animations api
//2do: test values against "undefined" values
//2do: more docs
