package main

import (
	"github.com/gopherjs/gopherjs/js"
	"github.com/gopherjs/jquery"
	QUnit "github.com/rusco/qunit"
	"strconv"
	"strings"
	"time"
)

const (
	SHOWCONSOLE = false
	FIX         = "#qunit-fixture"
	ROOT        = "http://localhost:3000"
)

type (
	Object      map[string]interface{}
	EvtScenario struct{}
)

func (s EvtScenario) Setup() {
	//Dear QUnit.js, how to add params to Setup Function for reusability ?
	jQuery(`<p id="firstp">See 
			<a id="someid" href="somehref" rel="bookmark">this blog entry</a>
			for more information.</p>`).AppendTo(FIX)
}
func (s EvtScenario) Teardown() {
	jQuery(FIX).Empty()
}

var jQuery = jquery.NewJQuery //convenience

func getDocumentBody() *js.Object {
	return js.Global.Get("document").Get("body")
}

func stringify(i interface{}) string {
	return js.Global.Get("JSON").Call("stringify", i).String()
}

func log(i ...interface{}) {
	js.Global.Get("console").Call("log", i...)
}

type working struct {
	jquery.Deferred
}

func NewWorking(d jquery.Deferred) working {
	return working{d}
}

func (w working) notify() {
	if w.State() == "pending" {
		w.Notify("working... ")
		js.Global.Call("setTimeout", w.notify, 500)
	}
}
func (w working) hi(name string) {
	if name == "John" {
		countJohn += 1
	} else if name == "Karl" {
		countKarl += 1
	}
	if SHOWCONSOLE {
		log("welcome message:", name)
	}
}

var (
	countJohn = 0
	countKarl = 0
)

func asyncEvent(accept bool, i int) *js.Object {

	dfd := jquery.NewDeferred()

	if accept {
		js.Global.Call("setTimeout", func() {
			dfd.Resolve("hurray")
		}, 200*i)
	} else {
		js.Global.Call("setTimeout", func() {
			dfd.Reject("sorry")
		}, 210*i)
	}

	wx := NewWorking(dfd)
	js.Global.Call("setTimeout", wx.notify, 1)

	return dfd.Promise()
}

func main() {

	QUnit.Module("Core")
	QUnit.Test("jQuery Properties", func(assert QUnit.QUnitAssert) {

		assert.Equal(jQuery().Jquery, "2.1.1", "JQuery Version")
		assert.Equal(jQuery().Length, 0, "jQuery().Length")

		jQ2 := jQuery("body")
		assert.Equal(jQ2.Selector, "body", `jQ2 := jQuery("body"); jQ2.Selector.Selector`)
		assert.Equal(jQuery("body").Selector, "body", `jQuery("body").Selector`)
	})

	//start dom tests
	QUnit.Test("Test Setup", func(assert QUnit.QUnitAssert) {

		test := jQuery(getDocumentBody()).Find(FIX)
		assert.Equal(test.Selector, FIX, "#qunit-fixture find Selector")
		assert.Equal(test.Context, getDocumentBody(), "#qunit-fixture find Context")
	})

	QUnit.Test("Static Functions", func(assert QUnit.QUnitAssert) {

		jquery.GlobalEval("var globalEvalTest = 2;")
		assert.Equal(js.Global.Get("globalEvalTest").Int(), 2, "GlobalEval: Test variable declarations are global")

		assert.Equal(jquery.Trim("  GopherJS  "), "GopherJS", "Trim: leading and trailing space")

		assert.Equal(jquery.Type(true), "boolean", "Type: Boolean")
		assert.Equal(jquery.Type(time.Now()), "date", "Type: Date")
		assert.Equal(jquery.Type("GopherJS"), "string", "Type: String")
		assert.Equal(jquery.Type(12.21), "number", "Type: Number")
		assert.Equal(jquery.Type(nil), "null", "Type: Null")
		assert.Equal(jquery.Type([2]string{"go", "lang"}), "array", "Type: Array")
		assert.Equal(jquery.Type([]string{"go", "lang"}), "array", "Type: Array")
		o := map[string]interface{}{"a": true, "b": 1.1, "c": "more"}
		assert.Equal(jquery.Type(o), "object", "Type: Object")
		assert.Equal(jquery.Type(getDocumentBody), "function", "Type: Function")

		assert.Ok(!jquery.IsPlainObject(""), "IsPlainObject: string")
		assert.Ok(jquery.IsPlainObject(o), "IsPlainObject: Object")
		assert.Ok(!jquery.IsEmptyObject(o), "IsEmptyObject: Object")
		assert.Ok(jquery.IsEmptyObject(map[string]interface{}{}), "IsEmptyObject: Object")

		assert.Ok(!jquery.IsFunction(""), "IsFunction: string")
		assert.Ok(jquery.IsFunction(getDocumentBody), "IsFunction: getDocumentBody")

		assert.Ok(!jquery.IsNumeric("a3a"), "IsNumeric: string")
		assert.Ok(jquery.IsNumeric("0xFFF"), "IsNumeric: hex")
		assert.Ok(jquery.IsNumeric("8e-2"), "IsNumeric: exponential")

		assert.Ok(!jquery.IsXMLDoc(getDocumentBody), "HTML Body element")
		assert.Ok(jquery.IsWindow(js.Global), "window")

	})

	QUnit.Test("ToArray,InArray", func(assert QUnit.QUnitAssert) {

		jQuery(`<div>a</div>
				<div>b</div>
				<div>c</div>`).AppendTo(FIX)

		divs := jQuery(FIX).Find("div")
		assert.Equal(divs.Length, 3, "3 divs in Fixture inserted")

		str := ""
		for _, v := range divs.ToArray() {
			str += jQuery(v).Text()
		}
		assert.Equal(str, "abc", "ToArray() allows range over selection")

		arr := []interface{}{"a", 3, true, 2.2, "GopherJS"}
		assert.Equal(jquery.InArray(4, arr), -1, "InArray")
		assert.Equal(jquery.InArray(3, arr), 1, "InArray")
		assert.Equal(jquery.InArray("a", arr), 0, "InArray")
		assert.Equal(jquery.InArray("b", arr), -1, "InArray")
		assert.Equal(jquery.InArray("GopherJS", arr), 4, "InArray")

	})

	QUnit.Test("ParseHTML, ParseXML, ParseJSON", func(assert QUnit.QUnitAssert) {

		str := `<ul>
  				<li class="firstclass">list item 1</li>
  				<li>list item 2</li>
  				<li>list item 3</li>
  				<li>list item 4</li>
  				<li class="lastclass">list item 5</li>
				</ul>`

		arr := jquery.ParseHTML(str)
		jQuery(arr).AppendTo(FIX)
		assert.Equal(jQuery(FIX).Find("ul li").Length, 5, "ParseHTML")

		xml := "<rss version='2.0'><channel><title>RSS Title</title></channel></rss>"
		xmlDoc := jquery.ParseXML(xml)
		assert.Equal(jQuery(xmlDoc).Find("title").Text(), "RSS Title", "ParseXML")

		obj := jquery.ParseJSON(`{ "language": "go" }`)
		language := obj.(map[string]interface{})["language"].(string)
		assert.Equal(language, "go", "ParseJSON")

	})

	QUnit.Test("Grep", func(assert QUnit.QUnitAssert) {

		arr := []interface{}{1, 9, 3, 8, 6, 1, 5, 9, 4, 7, 3, 8, 6, 9, 1}
		arr2 := jquery.Grep(arr, func(n interface{}, idx int) bool {
			return n.(float64) != float64(5) && idx > 4
		})
		assert.Equal(len(arr2), 9, "Grep")

	})

	QUnit.Test("Noop,Now", func(assert QUnit.QUnitAssert) {

		callSth := func(fn func() interface{}) interface{} {
			return fn()
		}
		_ = callSth(jquery.Noop)
		_ = jquery.Noop()
		assert.Ok(jquery.IsFunction(jquery.Noop), "jquery.Noop")

		date := js.Global.Get("Date").New()
		time := date.Call("getTime").Float()

		assert.Ok(time <= jquery.Now(), "jquery.Now()")
	})

	QUnit.Module("Dom")
	QUnit.Test("AddClass,Clone,Add,AppendTo,Find", func(assert QUnit.QUnitAssert) {

		jQuery("p").AddClass("wow").Clone().Add("<span id='dom02'>WhatADay</span>").AppendTo(FIX)
		txt := jQuery(FIX).Find("span#dom02").Text()
		assert.Equal(txt, "WhatADay", "Test of Clone, Add, AppendTo, Find, Text Functions")

		jQuery(FIX).Empty()

		html := `
			<div>This div should be white</div>
			<div class="red">This div will be green because it now has the "green" and "red" classes.
			   It would be red if the addClass function failed.</div>
			<div>This div should be white</div>
			<p>There are zero green divs</p>

			<button>some btn</button>`
		jQuery(html).AppendTo(FIX)
		jQuery(FIX).Find("div").AddClass(func(index int, currentClass string) string {

			addedClass := ""
			if currentClass == "red" {
				addedClass = "green"
				jQuery("p").SetText("There is one green div")
			}
			return addedClass
		})
		jQuery(FIX).Find("button").AddClass("red")
		assert.Ok(jQuery(FIX).Find("button").HasClass("red"), "button hasClass red")
		assert.Ok(jQuery(FIX).Find("p").Text() == "There is one green div", "There is one green div")
		assert.Ok(jQuery(FIX).Find("div:eq(1)").HasClass("green"), "one div hasClass green")
		jQuery(FIX).Empty()

	})
	QUnit.Test("Children,Append", func(assert QUnit.QUnitAssert) {

		var j = jQuery(`<div class="pipe animated"><div class="pipe_upper" style="height: 79px;"></div><div class="guess top" style="top: 114px;"></div><div class="pipe_middle" style="height: 100px; top: 179px;"></div><div class="guess bottom" style="bottom: 76px;"></div><div class="pipe_lower" style="height: 41px;"></div><div class="question"></div></div>`)
		assert.Ok(len(j.Html()) == 301, "jQuery html len")

		j.Children(".question").Append(jQuery(`<div class = "question_digit first" style = "background-image: url('assets/font_big_3.png');"></div>`))
		assert.Ok(len(j.Html()) == 397, "jquery html len after 1st jquery object append")

		j.Children(".question").Append(jQuery(`<div class = "question_digit symbol" style="background-image: url('assets/font_shitty_x.png');"></div>`))
		assert.Ok(len(j.Html()) == 497, "jquery htm len after 2nd jquery object append")

		j.Children(".question").Append(`<div class = "question_digit second" style = "background-image: url('assets/font_big_1.png');"></div>`)
		assert.Ok(len(j.Html()) == 594, "jquery html len after html append")

	})

	QUnit.Test("ApiOnly:ScollFn,SetCss,CssArray,FadeOut", func(assert QUnit.QUnitAssert) {

		//QUnit.Expect(0)
		for i := 0; i < 3; i++ {
			jQuery("p").Clone().AppendTo(FIX)
		}
		jQuery(FIX).Scroll(func(e jquery.Event) {
			jQuery("span").SetCss("display", "inline").FadeOut("slow")
		})

		htmlsnippet := `<style>
			  div {
			    height: 50px;
			    margin: 5px;
			    padding: 5px;
			    float: left;
			  }
			  #box1 {
			    width: 50px;
			    color: yellow;
			    background-color: blue;
			  }
			  #box2 {
			    width: 80px;
			    color: rgb(255, 255, 255);
			    background-color: rgb(15, 99, 30);
			  }
			  #box3 {
			    width: 40px;
			    color: #fcc;
			    background-color: #123456;
			  }
			  #box4 {
			    width: 70px;
			    background-color: #f11;
			  }
			  </style>
			 
			<p id="result">&nbsp;</p>
			<div id="box1">1</div>
			<div id="box2">2</div>
			<div id="box3">3</div>
			<div id="box4">4</div>`

		jQuery(htmlsnippet).AppendTo(FIX)

		jQuery(FIX).Find("div").On("click", func(evt jquery.Event) {

			html := []string{"The clicked div has the following styles:"}
			var styleProps = jQuery(evt.Target).CssArray("width", "height")
			for prop, value := range styleProps {
				html = append(html, prop+": "+value.(string))
			}
			jQuery(FIX).Find("#result").SetHtml(strings.Join(html, "<br>"))
		})
		jQuery(FIX).Find("div:eq(0)").Trigger("click")
		assert.Ok(jQuery(FIX).Find("#result").Html() == "The clicked div has the following styles:<br>width: 50px<br>height: 50px", "CssArray read properties")

	})

	QUnit.Test("ApiOnly:SelectFn,SetText,Show,FadeOut", func(assert QUnit.QUnitAssert) {

		QUnit.Expect(0)
		jQuery(`<p>Click and drag the mouse to select text in the inputs.</p>
  				<input type="text" value="Some text">
  				<input type="text" value="to test on">
  				<div></div>`).AppendTo(FIX)

		jQuery(":input").Select(func(e jquery.Event) {
			jQuery("div").SetText("Something was selected").Show().FadeOut("1000")
		})
	})

	QUnit.Test("Eq,Find", func(assert QUnit.QUnitAssert) {

		jQuery(`<div></div>
				<div></div>
				<div class="test"></div>
				<div></div>
				<div></div>
				<div></div>`).AppendTo(FIX)

		assert.Ok(jQuery(FIX).Find("div").Eq(2).HasClass("test"), "Eq(2) has class test")
		assert.Ok(!jQuery(FIX).Find("div").Eq(0).HasClass("test"), "Eq(0) has no class test")
	})

	QUnit.Test("Find,End", func(assert QUnit.QUnitAssert) {

		jQuery(`<p class='ok'><span class='notok'>Hello</span>, how are you?</p>`).AppendTo(FIX)

		assert.Ok(jQuery(FIX).Find("p").Find("span").HasClass("notok"), "before call to end")
		assert.Ok(jQuery(FIX).Find("p").Find("span").End().HasClass("ok"), "after call to end")
	})

	QUnit.Test("Slice,Attr,First,Last", func(assert QUnit.QUnitAssert) {

		jQuery(`<ul>
  				<li class="firstclass">list item 1</li>
  				<li>list item 2</li>
  				<li>list item 3</li>
  				<li>list item 4</li>
  				<li class="lastclass">list item 5</li>
				</ul>`).AppendTo(FIX)

		assert.Equal(jQuery(FIX).Find("li").Slice(2).Length, 3, "Slice")
		assert.Equal(jQuery(FIX).Find("li").Slice(2, 4).Length, 2, "SliceByEnd")

		assert.Equal(jQuery(FIX).Find("li").First().Attr("class"), "firstclass", "First")
		assert.Equal(jQuery(FIX).Find("li").Last().Attr("class"), "lastclass", "Last")

	})

	QUnit.Test("Css", func(assert QUnit.QUnitAssert) {

		jQuery(FIX).SetCss(map[string]interface{}{"color": "red", "background": "blue", "width": "20px", "height": "10px"})
		assert.Ok(jQuery(FIX).Css("width") == "20px" && jQuery(FIX).Css("height") == "10px", "SetCssMap")

		div := jQuery("<div style='display: inline'/>").Show().AppendTo(FIX)
		assert.Equal(div.Css("display"), "inline", "Make sure that element has same display when it was created.")
		div.Remove()

		span := jQuery("<span/>").Hide().Show()
		assert.Equal(span.Get(0).Get("style").Get("display"), "inline", "For detached span elements, display should always be inline")
		span.Remove()

	})

	QUnit.Test("Attributes", func(assert QUnit.QUnitAssert) {

		jQuery("<form id='testForm'></form>").AppendTo(FIX)
		extras := jQuery("<input id='id' name='id' /><input id='name' name='name' /><input id='target' name='target' />").AppendTo("#testForm")
		assert.Equal(jQuery("#testForm").Attr("target"), "", "Attr")
		assert.Equal(jQuery("#testForm").SetAttr("target", "newTarget").Attr("target"), "newTarget", "SetAttr2")
		assert.Equal(jQuery("#testForm").RemoveAttr("id").Attr("id"), "", "RemoveAttr ")
		assert.Equal(jQuery("#testForm").Attr("name"), "", "Attr undefined")
		extras.Remove()

		jQuery("<a/>").SetAttr(map[string]interface{}{"id": "tAnchor5", "href": "#5"}).AppendTo(FIX)
		assert.Equal(jQuery("#tAnchor5").Attr("href"), "#5", "Attr")
		jQuery("<a id='tAnchor6' href='#5' />").AppendTo(FIX)
		assert.Equal(jQuery("#tAnchor5").Prop("href"), jQuery("#tAnchor6").Prop("href"), "Prop")

		input := jQuery("<input name='tester' />")
		assert.StrictEqual(input.Clone(true).SetAttr("name", "test").Underlying().Index(0).Get("name"), "test", "Clone")

		jQuery(FIX).Empty()

		jQuery(`<input type="checkbox" checked="checked">
  			<input type="checkbox">
  			<input type="checkbox">
  			<input type="checkbox" checked="checked">`).AppendTo(FIX)

		jQuery(FIX).Find("input[type='checkbox']").SetProp("disabled", true)
		assert.Ok(jQuery(FIX).Find("input[type='checkbox']").Prop("disabled"), "SetProp")

	})

	QUnit.Test("Unique", func(assert QUnit.QUnitAssert) {

		jQuery(`<div>There are 6 divs in this document.</div>
				<div></div>
				<div class="dup"></div>
				<div class="dup"></div>
				<div class="dup"></div>
				<div></div>`).AppendTo(FIX)

		divs := jQuery(FIX).Find("div").Get()
		assert.Equal(divs.Get("length"), 6, "6 divs inserted")

		jQuery(FIX).Find(".dup").Clone(true).AppendTo(FIX)
		divs2 := jQuery(FIX).Find("div").Get()
		assert.Equal(divs2.Get("length"), 9, "9 divs inserted")

		divs3 := jquery.Unique(divs)
		assert.Equal(divs3.Get("length"), 6, "post-qunique should be 6 elements")
	})

	QUnit.Test("Serialize,SerializeArray,Trigger,Submit", func(assert QUnit.QUnitAssert) {

		QUnit.Expect(2)
		jQuery(`<form>
				  <div><input type="text" name="a" value="1" id="a"></div>
				  <div><input type="text" name="b" value="2" id="b"></div>
				  <div><input type="hidden" name="c" value="3" id="c"></div>
				  <div>
				    <textarea name="d" rows="8" cols="40">4</textarea>
				  </div>
				  <div><select name="e">
				    <option value="5" selected="selected">5</option>
				    <option value="6">6</option>
				    <option value="7">7</option>
				  </select></div>
				  <div>
				    <input type="checkbox" name="f" value="8" id="f">
				  </div>
				  <div>
				    <input type="submit" name="g" value="Submit" id="g">
				  </div>
				</form>`).AppendTo(FIX)

		var collectResults string
		jQuery(FIX).Find("form").Submit(func(evt jquery.Event) {

			sa := jQuery(evt.Target).SerializeArray()
			for i := 0; i < sa.Length(); i++ {
				collectResults += sa.Index(i).Get("name").String()
			}
			assert.Equal(collectResults, "abcde", "SerializeArray")
			evt.PreventDefault()
		})

		serializedString := "a=1&b=2&c=3&d=4&e=5"
		assert.Equal(jQuery(FIX).Find("form").Serialize(), serializedString, "Serialize")

		jQuery(FIX).Find("form").Trigger("submit")
	})

	QUnit.ModuleLifecycle("Events", EvtScenario{})
	QUnit.Test("On,One,Off,Trigger", func(assert QUnit.QUnitAssert) {

		fn := func(ev jquery.Event) {
			assert.Ok(ev.Data != js.Undefined, "on() with data, check passed data exists")
			assert.Equal(ev.Data.Get("foo"), "bar", "on() with data, Check value of passed data")
		}

		data := map[string]interface{}{"foo": "bar"}
		jQuery("#firstp").On(jquery.CLICK, data, fn).Trigger(jquery.CLICK).Off(jquery.CLICK, fn)

		var clickCounter, mouseoverCounter int
		handler := func(ev jquery.Event) {
			if ev.Type == jquery.CLICK {
				clickCounter++
			} else if ev.Type == jquery.MOUSEOVER {
				mouseoverCounter++
			}
		}

		handlerWithData := func(ev jquery.Event) {
			if ev.Type == jquery.CLICK {
				clickCounter += ev.Data.Get("data").Int()
			} else if ev.Type == jquery.MOUSEOVER {
				mouseoverCounter += ev.Data.Get("data").Int()
			}
		}

		data2 := map[string]interface{}{"data": 2}
		elem := jQuery("#firstp").On(jquery.CLICK, handler).On(jquery.MOUSEOVER, handler).One(jquery.CLICK, data2, handlerWithData).One(jquery.MOUSEOVER, data2, handlerWithData)
		assert.Equal(clickCounter, 0, "clickCounter initialization ok")
		assert.Equal(mouseoverCounter, 0, "mouseoverCounter initialization ok")

		elem.Trigger(jquery.CLICK).Trigger(jquery.MOUSEOVER)
		assert.Equal(clickCounter, 3, "clickCounter Increased after Trigger/On/One")
		assert.Equal(mouseoverCounter, 3, "mouseoverCounter Increased after Trigger/On/One")

		elem.Trigger(jquery.CLICK).Trigger(jquery.MOUSEOVER)
		assert.Equal(clickCounter, 4, "clickCounter Increased after Trigger/On")
		assert.Equal(mouseoverCounter, 4, "a) mouseoverCounter Increased after TriggerOn")

		elem.Trigger(jquery.CLICK).Trigger(jquery.MOUSEOVER)
		assert.Equal(clickCounter, 5, "b) clickCounter not Increased after Off")
		assert.Equal(mouseoverCounter, 5, "c) mouseoverCounter not Increased after Off")

		elem.Off(jquery.CLICK).Off(jquery.MOUSEOVER)
		//2do: elem.Off(jquery.CLICK, handlerWithData).Off(jquery.MOUSEOVER, handlerWithData)
		elem.Trigger(jquery.CLICK).Trigger(jquery.MOUSEOVER)
		assert.Equal(clickCounter, 5, "clickCounter not Increased after Off")
		assert.Equal(mouseoverCounter, 5, "mouseoverCounter not Increased after Off")

	})
	QUnit.Test("Each", func(assert QUnit.QUnitAssert) {

		jQuery(FIX).Empty()

		html := `<style>
			  		div {
			    		color: red;
			    		text-align: center;
			    		cursor: pointer;
			    		font-weight: bolder;
				    width: 300px;
				  }
				 </style>
				 <div>Click here</div>
				 <div>to iterate through</div>
				 <div>these divs.</div>`

		jQuery(html).AppendTo(FIX)
		blueCount := 0

		jQuery(FIX).On(jquery.CLICK, func(e jquery.Event) {

			//jQuery(FIX).Find("div").Each(func(i int, elem interface{}) interface{} {
			jQuery(FIX).Find("div").Each(func(i int, elem interface{}) {

				style := jQuery(elem).Get(0).Get("style")
				if style.Get("color").String() != "blue" {
					style.Set("color", "blue")
				} else {
					blueCount += 1
					style.Set("color", "")
				}

			})
		})
		for i := 0; i < 6; i++ {
			jQuery(FIX).Find("div:eq(0)").Trigger("click")

		}
		assert.Equal(jQuery(FIX).Find("div").Length, 3, "Test setup problem: 3 divs expected")
		assert.Equal(blueCount, 9, "blueCount Counter should be 9")
	})

	QUnit.Test("Filter, Resize", func(assert QUnit.QUnitAssert) {

		jQuery(FIX).Empty()
		html := `<style>
				  	div {
				    	width: 60px;
				    	height: 60px;
				    	margin: 5px;
				    	float: left;
				    	border: 2px white solid;
				  	}
				 </style>
				  
				 <div></div>
				 <div class="middle"></div>
				 <div class="middle"></div>
				 <div class="middle"></div>
				 <div class="middle"></div>
				 <div></div>`

		jQuery(html).AppendTo(FIX)

		jQuery(FIX).Find("div").SetCss("background", "silver").Filter(func(index int) bool {
			return index%3 == 2
		}).SetCss("font-weight", "bold")

		countFontweight := 0
		jQuery(FIX).Find("div").Each(func(i int, elem interface{}) {

			fw := jQuery(elem).Css("font-weight")
			if fw == "bold" || fw == "700" {
				countFontweight += 1
			}

		})
		assert.Equal(countFontweight, 2, "2 divs should have font-weight = 'bold'")

		jQuery(js.Global).Resize(func() {
			jQuery(FIX).Find("div:eq(0)").SetText(strconv.Itoa(jQuery("div:eq(0)").Width()))
		}).Resize()
		assert.Equal(jQuery(FIX).Find("div:eq(0)").Text(), "60", "text of first div should be 60")

	})

	QUnit.Test("Not,Offset", func(assert QUnit.QUnitAssert) {

		QUnit.Expect(0) //api test only

		jQuery(FIX).Empty()
		html := `<div></div>
				 <div id="blueone"></div>
				 <div></div>
				 <div class="green"></div>
				 <div class="green"></div>
				 <div class="gray"></div>
				 <div></div>`
		jQuery(html).AppendTo(FIX)
		jQuery(FIX).Find("div").Not(".green,#blueone").SetCss("border-color", "red")

		jQuery("*", "body").On("click", func(event jquery.Event) {
			offset := jQuery(event.Target).Offset()
			event.StopPropagation()
			tag := jQuery(event.Target).Prop("tagName").(string)
			jQuery("#result").SetText(tag + " coords ( " + strconv.Itoa(offset.Left) + ", " + strconv.Itoa(offset.Top) + " )")
		})
	})

	QUnit.Module("Ajax")
	QUnit.AsyncTest("Async Dummy Test", func() interface{} {
		QUnit.Expect(1)

		return js.Global.Call("setTimeout", func() {
			QUnit.Ok(true, " async ok")
			QUnit.Start()
		}, 1000)

	})

	QUnit.AsyncTest("Ajax Call", func() interface{} {

		QUnit.Expect(1)

		ajaxopt := Object{
			"async":       true,
			"type":        "POST",
			"url":         ROOT + "/nestedjson/",
			"contentType": "application/json charset=utf-8",
			"dataType":    "json",
			"data":        nil,
			"beforeSend": func(data Object) {
				if SHOWCONSOLE {
					print(" before:", data)
				}
			},
			"success": func(data Object) {

				dataStr := stringify(data)
				expected := `{"message":"Welcome!","nested":{"level":1,"moresuccess":true},"success":true}`

				QUnit.Ok(dataStr == expected, "Ajax call did not returns expected result")
				QUnit.Start()

				if SHOWCONSOLE {
					print(" ajax call success:", data)
					for k, v := range data {
						switch v.(type) {
						case bool:
							print(k, v.(bool))
						case string:
							print(k, v.(string))
						case float64:
							print(k, v.(float64))
						default:
							print("sth. else:", k, v)
						}
					}
				}
			},
			"error": func(status interface{}) {
				if SHOWCONSOLE {
					print(" ajax call error:", status)
				}
			},
		}
		//ajax call:
		jquery.Ajax(ajaxopt)
		return nil
	})

	QUnit.AsyncTest("Load", func() interface{} {

		QUnit.Expect(1)
		jQuery(FIX).Load("/resources/load.html", func() {
			if SHOWCONSOLE {
				print(" load got: ", jQuery(FIX).Html() == `<div>load successful!</div>`)
			}
			QUnit.Ok(jQuery(FIX).Html() == `<div>load successful!</div>`, "Load call did not returns expected result")
			QUnit.Start()
		})
		return nil
	})

	QUnit.AsyncTest("Get", func() interface{} {
		QUnit.Expect(1)

		jquery.Get("/resources/get.html", func(data interface{}, status string, xhr interface{}) {
			if SHOWCONSOLE {
				print(" data:   ", data)
				print(" status: ", status)
				print(" xhr:    ", xhr)
			}
			QUnit.Ok(data == `<div>get successful!</div>`, "Get call did not returns expected result")
			QUnit.Start()
		})
		return nil
	})

	QUnit.AsyncTest("Post", func() interface{} {
		QUnit.Expect(1)
		jquery.Post("/gopher", func(data interface{}, status string, xhr interface{}) {
			if SHOWCONSOLE {
				print(" data:   ", data)
				print(" status: ", status)
				print(" xhr:    ", xhr)
			}
			QUnit.Ok(data == `<div>Welcome gopher</div>`, "Post call did not returns expected result")
			QUnit.Start()
		})
		return nil
	})

	QUnit.AsyncTest("GetJSON", func() interface{} {
		QUnit.Expect(1)
		jquery.GetJSON("/json/1", func(data interface{}) {
			if val, ok := data.(map[string]interface{})["json"]; ok {
				if SHOWCONSOLE {
					print("GetJSON call returns: ", val)
				}
				QUnit.Ok(val == `1`, "Json call did not returns expected result")
				QUnit.Start()
			}
		})
		return nil
	})

	QUnit.AsyncTest("GetScript", func() interface{} {
		QUnit.Expect(1)

		jquery.GetScript("/script", func(data interface{}) {
			if SHOWCONSOLE {
				print("GetScript call returns script of length: ", len(data.(string)))
			}

			QUnit.Ok(len(data.(string)) == 29, "GetScript call did not returns expected result")
			QUnit.Start()

		})
		return nil
	})

	QUnit.AsyncTest("AjaxSetup", func() interface{} {
		QUnit.Expect(1)

		ajaxSetupOptions := Object{
			"async":       true,
			"type":        "POST",
			"url":         "/nestedjson/",
			"contentType": "application/json charset=utf-8",
		}

		jquery.AjaxSetup(ajaxSetupOptions)

		ajaxopt := Object{
			"dataType": "json",
			"data":     nil,
			"beforeSend": func(data Object) {
				if SHOWCONSOLE {
					print(" ajaxSetup call, before:", data)
				}
			},
			"success": func(data Object) {

				dataStr := stringify(data)
				expected := `{"message":"Welcome!","nested":{"level":1,"moresuccess":true},"success":true}`

				QUnit.Ok(dataStr == expected, "AjaxSetup call did not returns expected result")
				QUnit.Start()

				if SHOWCONSOLE {
					print(" ajaxSetup call success:", data)
					for k, v := range data {
						switch v.(type) {
						case bool:
							print(k, v.(bool))
						case string:
							print(k, v.(string))
						case float64:
							print(k, v.(float64))
						default:
							print("sth. else:", k, v)
						}
					}
				}
			},
			"error": func(status interface{}) {
				if SHOWCONSOLE {
					print(" ajaxSetup Call error:", status)
				}
			},
		}
		//ajax
		jquery.Ajax(ajaxopt)

		return nil
	})

	QUnit.AsyncTest("AjaxPrefilter", func() interface{} {
		QUnit.Expect(1)
		jquery.AjaxPrefilter("+json", func(options interface{}, originalOptions string, jqXHR interface{}) {
			if SHOWCONSOLE {
				print(" ajax prefilter options:", options.(map[string]interface{})["url"].(string))
			}
			//API Test only
		})

		jquery.GetJSON("/json/3", func(data interface{}) {
			if val, ok := data.(map[string]interface{})["json"]; ok {
				if SHOWCONSOLE {
					print("ajaxPrefilter result: ", val.(string))
				}
				QUnit.Ok(val.(string) == "3", "AjaxPrefilter call did not returns expected result")
				QUnit.Start()
			}
		})
		return nil
	})

	QUnit.AsyncTest("AjaxTransport", func() interface{} {
		QUnit.Expect(1)

		jquery.AjaxTransport("+json", func(options interface{}, originalOptions string, jqXHR interface{}) {
			if SHOWCONSOLE {
				print(" ajax transport options:", options)
			}
			//API Test only
		})

		jquery.GetJSON("/json/4", func(data interface{}) {
			if val, ok := data.(map[string]interface{})["json"]; ok {
				QUnit.Ok(val.(string) == "4", "AjaxTransport call did not returns expected result")
				QUnit.Start()
			}
		})
		return nil
	})

	QUnit.Module("Deferreds")
	QUnit.AsyncTest("Deferreds Test 01", func() interface{} {

		QUnit.Expect(1)

		pass, fail, progress := 0, 0, 0
		for i := 0; i < 10; i++ {
			jquery.When(asyncEvent(i%2 == 0, i)).Then(

				func(status interface{}) {
					if SHOWCONSOLE {
						log(status, "things are going well")
					}
					pass += 1
				},
				func(status interface{}) {
					if SHOWCONSOLE {
						log(status, ", you fail this time")
					}
					fail += 1
				},
				func(status interface{}) {
					if SHOWCONSOLE {
						log("Progress: ", status.(string))
					}
					progress += 1
				},
			).Done(func() {
				if SHOWCONSOLE {
					log(" Done. pass, fail, notify = ", pass, fail, progress)
				}
				if pass >= 5 {
					QUnit.Start()
					QUnit.Ok(pass >= 5 && fail >= 4 && progress >= 20, "Deferred Test 01 fail")
				}

			})
		}
		return nil
	})

	QUnit.Test("Deferreds Test 02", func(assert QUnit.QUnitAssert) {

		QUnit.Expect(1)

		o := NewWorking(jquery.NewDeferred())
		o.Resolve("John")

		o.Done(func(name string) {
			o.hi(name)
		}).Done(func(name string) {
			o.hi("John")
			if SHOWCONSOLE {
				log(" test 02 done: ", countJohn /*2*/, countKarl /*0*/)
			}
		})
		o.hi("Karl")
		if SHOWCONSOLE {
			log(" test 02 end : ", countJohn /*2*/, countKarl /*1*/)
		}
		assert.Ok(countJohn == 2 && countKarl == 1, "Deferred Test 02 fail")
	})

	QUnit.AsyncTest("Deferreds Test 03", func() interface{} {

		QUnit.Expect(1)
		jquery.Get("/get.html").Always(func() {
			if SHOWCONSOLE {
				log("TEST 03: $.get completed with success or error callback arguments")
			}
			QUnit.Start()
			QUnit.Ok(true, "Deferred Test 03 fail")
		})
		return nil
	})

	QUnit.AsyncTest("Deferreds Test 04", func() interface{} {

		QUnit.Expect(2)
		jquery.Get("/get.html").Done(func() {
			QUnit.Ok(true, "Deferred Test 04 fail")
			if SHOWCONSOLE {
				log("$.get done:  test 04")
			}
		}).Fail(func() {
			if SHOWCONSOLE {
				log("$.get fail:  test 04")
			}
		})
		jquery.Get("/shouldnotexist.html").Done(func() {
			if SHOWCONSOLE {
				log("$.get done:  test 04 part 2")
			}
		}).Fail(func() {
			if SHOWCONSOLE {
				log("$.get fail: test 04 part 2")
			}
			QUnit.Start()
			QUnit.Ok(true, "Deferred Test 04 fail")
		})
		return nil
	})

	QUnit.AsyncTest("Deferreds Test 05", func() interface{} {

		QUnit.Expect(2)
		jquery.Get("/get.html").Then(func() {
			if SHOWCONSOLE {
				log("$.get done:  with success, test 05")
			}
			QUnit.Ok(true, "Deferred Test 05 fail")

		}, func() {
			if SHOWCONSOLE {
				log("$.get fail:  test 05 part 1")
			}
		})
		jquery.Get("/shouldnotexist.html").Then(func() {
			if SHOWCONSOLE {
				log("$.get done:  test 05 part 2")
			}

		}, func() {
			if SHOWCONSOLE {
				log("$.get fail:  test 05 part 1")
			}
			QUnit.Start()
			QUnit.Ok(true, "Deferred Test 05, 2nd part, fail")
		})
		return nil
	})

	QUnit.Test("Deferreds Test 06", func(assert QUnit.QUnitAssert) {

		QUnit.Expect(1)
		o := jquery.NewDeferred()

		filtered := o.Then(func(value int) int {
			return value * 2
		})
		o.Resolve(5)
		filtered.Done(func(value int) {
			if SHOWCONSOLE {
				log("test 06: value is ( 2*5 ) = ", value)
			}
			assert.Ok(value == 10, "Deferred Test 06 fail")
		})
	})

	QUnit.Test("Deferreds Test 07", func(assert QUnit.QUnitAssert) {

		o := jquery.NewDeferred()
		filtered := o.Then(nil, func(value int) int {
			return value * 3
		})
		o.Reject(6)
		filtered.Fail(func(value int) {
			if SHOWCONSOLE {
				log("Value is ( 3*6 ) = ", value)
			}
			assert.Ok(value == 18, "Deferred Test 07 fail")
		})
	})

}
