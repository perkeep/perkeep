package main

import (
	"github.com/go-martini/martini"
	"github.com/martini-contrib/render"
	"go/build"
	"io/ioutil"
	"net/http"
)

func main() {
	m := martini.Classic()

	//serve test folder
	m.Use(martini.Static("test"))

	//serve sourcemaps from GOROOT and GOPATH
	m.Use(martini.Static(build.Default.GOROOT, martini.StaticOptions{Prefix: "goroot"}))
	m.Use(martini.Static(build.Default.GOPATH, martini.StaticOptions{Prefix: "gopath"}))
	m.Use(render.Renderer())

	m.Get("/json/:param1", func(args martini.Params, r render.Render) {
		r.JSON(200, map[string]interface{}{"json": args["param1"]})
	})

	m.Post("/nestedjson", func(r render.Render) {
		r.JSON(200, map[string]interface{}{"success": true, "message": "Welcome!", "nested": map[string]interface{}{"moresuccess": true, "level": 1}})
	})

	m.Get("/script", func(res http.ResponseWriter, req *http.Request) {
		res.Header().Set("Content-Type", "application/javascript")

		b, err := ioutil.ReadFile("test/resources/some.js")
		if err != nil {
			res.WriteHeader(500)
			return
		} else {
			res.WriteHeader(200)
		}
		res.Write(b)

	})

	m.Post("/:name", func(args martini.Params) string {
		return "<div>Welcome " + args["name"] + "</div>"
	})

	m.Run()
}
