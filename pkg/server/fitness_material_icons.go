/*
Copyright 2026 The Perkeep Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package server

import (
	"bytes"
	"embed"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"go4.org/legal"
)

// These are unmodified Google Material Design Rounded 24px SVGs. See
// materialicons/README.md for the pinned upstream revision and source paths.
// No activity data or network access is involved in loading them.
//
//go:embed materialicons/*.svg
var materialIconFiles embed.FS

//go:embed materialicons/LICENSE
var fitnessMaterialLicense string

func init() {
	legal.RegisterLicense("Google Material Design Icons\n\n" + fitnessMaterialLicense)
}

var fitnessMaterialNames = map[string]string{
	"run": "directions_run", "walk": "directions_walk", "hike": "hiking",
	"ride": "directions_bike", "swim": "pool", "row": "rowing",
	"ski": "downhill_skiing", "workout": "fitness_center", "other": "sports",
}

type fitnessMaterialOp struct {
	kind byte // Absolute M, L, C or Z; unused coordinates are zero.
	args [6]float64
}

type fitnessMaterialShape struct {
	path   []fitnessMaterialOp
	circle [3]float64 // cx, cy, r; positive r distinguishes circles.
}

type fitnessMaterialIcon struct {
	shapes []fitnessMaterialShape
}

var fitnessMaterialCache struct {
	once  sync.Once
	icons map[string]*fitnessMaterialIcon // Immutable after once completes.
	err   error
}

func fitnessMaterialIcons() (map[string]*fitnessMaterialIcon, error) {
	fitnessMaterialCache.once.Do(func() {
		icons := make(map[string]*fitnessMaterialIcon, len(fitnessMaterialNames))
		for sport, name := range fitnessMaterialNames {
			data, err := materialIconFiles.ReadFile("materialicons/" + name + ".svg")
			if err == nil {
				icons[sport], err = parseFitnessMaterialSVG(data)
			}
			if err != nil {
				fitnessMaterialCache.err = fmt.Errorf("Material icon %s: %w", name, err)
				return
			}
		}
		fitnessMaterialCache.icons = icons
	})
	return fitnessMaterialCache.icons, fitnessMaterialCache.err
}

func drawFitnessMaterialIcon(img *image.RGBA, pen fitnessPen, sport string, cx, cy, size float64) {
	icons, err := fitnessMaterialIcons()
	if err != nil {
		// An asset regression must fail the embedded-asset tests, not crash a
		// running server. The unchanged white badge is still safe to draw.
		return
	}
	icons[fitnessSport(sport)].draw(img, pen, cx, cy, size)
}

func (icon *fitnessMaterialIcon) draw(img *image.RGBA, pen fitnessPen, cx, cy, size float64) {
	scale := size * .75 / 24
	x, y := cx-12*scale, cy-12*scale
	point := func(a, b float64) (float32, float32) {
		return float32(x + a*scale), float32(y + b*scale)
	}
	for _, shape := range icon.shapes {
		if shape.circle[2] > 0 {
			// Two upstream SVGs contain native circles. Use the same cubic
			// circle rasterization as the badge, not a replacement glyph.
			pen.circle(x+shape.circle[0]*scale, y+shape.circle[1]*scale, shape.circle[2]*scale)
		} else {
			open := false
			for _, op := range shape.path {
				switch op.kind {
				case 'M':
					if open {
						pen.z.ClosePath()
					}
					pen.z.MoveTo(point(op.args[0], op.args[1]))
					open = true
				case 'L':
					pen.z.LineTo(point(op.args[0], op.args[1]))
					open = true
				case 'C':
					a, b := point(op.args[0], op.args[1])
					c, d := point(op.args[2], op.args[3])
					e, f := point(op.args[4], op.args[5])
					pen.z.CubeTo(a, b, c, d, e, f)
					open = true
				case 'Z':
					pen.z.ClosePath()
					open = false
				}
			}
			// SVG fill implicitly closes open subpaths (directions_walk has
			// one); vector.Rasterizer does not do this automatically.
			if open {
				pen.z.ClosePath()
			}
		}
		pen.paint(img, color.NRGBA{63, 80, 101, 255})
	}
}

var errFitnessMaterialSVG = errors.New("unsupported Material SVG")

// parseFitnessMaterialSVG deliberately accepts only the small SVG subset used
// by the trusted embedded assets. It is not a general SVG renderer: transforms,
// strokes, styles, external references and unexpected elements fail closed.
func parseFitnessMaterialSVG(data []byte) (*fitnessMaterialIcon, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	icon := new(fitnessMaterialIcon)
	depth, root := 0, false
	var parents []string
	for {
		token, err := dec.Token()
		if err == io.EOF {
			if !root || depth != 0 || len(icon.shapes) == 0 {
				return nil, errFitnessMaterialSVG
			}
			return icon, nil
		}
		if err != nil {
			return nil, err
		}
		switch e := token.(type) {
		case xml.StartElement:
			if e.Name.Space != "" && e.Name.Space != "http://www.w3.org/2000/svg" {
				return nil, errFitnessMaterialSVG
			}
			if depth > 0 && parents[depth-1] != "svg" && parents[depth-1] != "g" {
				return nil, errFitnessMaterialSVG
			}
			if depth == 0 {
				if root || e.Name.Local != "svg" {
					return nil, errFitnessMaterialSVG
				}
				attrs, err := fitnessMaterialAttrs(e, "xmlns width height viewBox enable-background")
				if err != nil || attrs["viewBox"] != "0 0 24 24" || attrs["width"] != "24" || attrs["height"] != "24" {
					return nil, errFitnessMaterialSVG
				}
				root = true
			} else {
				var attrs map[string]string
				var err error
				switch e.Name.Local {
				case "g":
					_, err = fitnessMaterialAttrs(e, "")
				case "path":
					attrs, err = fitnessMaterialAttrs(e, "d fill")
					if err == nil && attrs["fill"] != "none" {
						var ops []fitnessMaterialOp
						ops, err = parseFitnessMaterialPath(attrs["d"])
						icon.shapes = append(icon.shapes, fitnessMaterialShape{path: ops})
					}
				case "circle":
					attrs, err = fitnessMaterialAttrs(e, "cx cy r fill")
					if err == nil && attrs["fill"] != "none" {
						var circle [3]float64
						for i, name := range []string{"cx", "cy", "r"} {
							circle[i], err = strconv.ParseFloat(attrs[name], 64)
							if err != nil || !fitnessMaterialFinite(circle[i]) {
								return nil, errFitnessMaterialSVG
							}
						}
						if circle[2] <= 0 {
							return nil, errFitnessMaterialSVG
						}
						icon.shapes = append(icon.shapes, fitnessMaterialShape{circle: circle})
					}
				case "rect":
					attrs, err = fitnessMaterialAttrs(e, "width height x y fill")
					if attrs["fill"] != "none" {
						return nil, errFitnessMaterialSVG
					}
				default:
					return nil, errFitnessMaterialSVG
				}
				if err != nil {
					return nil, err
				}
			}
			depth++
			parents = append(parents, e.Name.Local)
		case xml.EndElement:
			depth--
			parents = parents[:depth]
		case xml.CharData:
			if strings.TrimSpace(string(e)) != "" {
				return nil, errFitnessMaterialSVG
			}
		case xml.Comment:
		default:
			return nil, errFitnessMaterialSVG
		}
	}
}

func fitnessMaterialAttrs(e xml.StartElement, allowed string) (map[string]string, error) {
	attrs := make(map[string]string, len(e.Attr))
	for _, a := range e.Attr {
		if a.Name.Space != "" || !strings.Contains(" "+allowed+" ", " "+a.Name.Local+" ") {
			return nil, errFitnessMaterialSVG
		}
		if _, exists := attrs[a.Name.Local]; exists {
			return nil, errFitnessMaterialSVG
		}
		if a.Name.Local == "fill" && a.Value != "none" {
			return nil, errFitnessMaterialSVG
		}
		attrs[a.Name.Local] = a.Value
	}
	return attrs, nil
}

// Keep numbers safely representable by the float32 vector rasterizer, including
// after relative-coordinate addition. Actual asset coordinates are around 0..24.
func fitnessMaterialFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && math.Abs(v) <= 1e6
}

var fitnessMaterialNumber = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?`)

type fitnessMaterialToken struct {
	command byte
	number  float64
}

func fitnessMaterialTokens(d string) ([]fitnessMaterialToken, error) {
	var tokens []fitnessMaterialToken
	for len(d) > 0 {
		d = strings.TrimLeft(d, " \t\r\n")
		if d == "" {
			break
		}
		if d[0] == ',' {
			if len(tokens) == 0 || tokens[len(tokens)-1].command != 0 {
				return nil, errFitnessMaterialSVG
			}
			d = strings.TrimLeft(d[1:], " \t\r\n")
			if d == "" || fitnessMaterialNumber.FindString(d) == "" {
				return nil, errFitnessMaterialSVG
			}
		}
		if strings.ContainsRune("MmLlHhVvCcSsZz", rune(d[0])) {
			tokens = append(tokens, fitnessMaterialToken{command: d[0]})
			d = d[1:]
			continue
		}
		n := fitnessMaterialNumber.FindString(d)
		if n == "" {
			return nil, errFitnessMaterialSVG
		}
		v, err := strconv.ParseFloat(n, 64)
		if err != nil || !fitnessMaterialFinite(v) {
			return nil, errFitnessMaterialSVG
		}
		tokens = append(tokens, fitnessMaterialToken{number: v})
		d = d[len(n):]
	}
	return tokens, nil
}

// parseFitnessMaterialPath converts the required SVG commands to absolute
// rasterizer operations, retaining upstream cubic curves without simplification.
func parseFitnessMaterialPath(d string) ([]fitnessMaterialOp, error) {
	tokens, err := fitnessMaterialTokens(d)
	if err != nil || len(tokens) == 0 || (tokens[0].command != 'M' && tokens[0].command != 'm') {
		return nil, errFitnessMaterialSVG
	}
	var ops []fitnessMaterialOp
	var cmd, previous byte
	var pos, start, control fitnessPoint
	for i := 0; i < len(tokens); {
		if tokens[i].command != 0 {
			cmd = tokens[i].command
			i++
		}
		if cmd == 0 {
			return nil, errFitnessMaterialSVG
		}
		upper := cmd
		relative := cmd >= 'a' && cmd <= 'z'
		if relative {
			upper -= 'a' - 'A'
		}
		if upper == 'Z' {
			ops = append(ops, fitnessMaterialOp{kind: 'Z'})
			pos, previous, cmd = start, 'Z', 0
			continue
		}
		count := 2
		switch upper {
		case 'H', 'V':
			count = 1
		case 'C':
			count = 6
		case 'S':
			count = 4
		}
		var a [6]float64
		for j := 0; j < count; j++ {
			if i >= len(tokens) || tokens[i].command != 0 {
				return nil, errFitnessMaterialSVG
			}
			a[j] = tokens[i].number
			i++
		}
		if relative {
			for j := 0; j < count; j++ {
				if upper == 'V' || j%2 == 1 {
					a[j] += pos.y
				} else {
					a[j] += pos.x
				}
			}
		}
		op := fitnessMaterialOp{kind: upper, args: a}
		switch upper {
		case 'M':
			pos = fitnessPoint{a[0], a[1]}
			start = pos
			cmd = 'L'
			if relative {
				cmd = 'l'
			}
		case 'L':
			pos = fitnessPoint{a[0], a[1]}
		case 'H':
			op.kind, op.args = 'L', [6]float64{a[0], pos.y}
			pos.x = a[0]
		case 'V':
			op.kind, op.args = 'L', [6]float64{pos.x, a[0]}
			pos.y = a[0]
		case 'S':
			reflected := pos
			if previous == 'C' || previous == 'S' {
				reflected = fitnessPoint{2*pos.x - control.x, 2*pos.y - control.y}
			}
			op.kind, op.args = 'C', [6]float64{reflected.x, reflected.y, a[0], a[1], a[2], a[3]}
			fallthrough
		case 'C':
			control = fitnessPoint{op.args[2], op.args[3]}
			pos = fitnessPoint{op.args[4], op.args[5]}
		}
		for _, v := range op.args {
			if !fitnessMaterialFinite(v) {
				return nil, errFitnessMaterialSVG
			}
		}
		ops = append(ops, op)
		previous = upper
	}
	return ops, nil
}
