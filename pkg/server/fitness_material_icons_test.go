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
	"context"
	"crypto/sha256"
	"fmt"
	"image"
	"image/png"
	"math"
	"reflect"
	"strings"
	"testing"

	"go4.org/legal"
	"golang.org/x/image/vector"
)

func TestFitnessMaterialAssets(t *testing.T) {
	t.Parallel()
	icons, err := fitnessMaterialIcons()
	if err != nil {
		t.Fatal(err)
	}
	wantNames := map[string]string{
		"run": "directions_run", "walk": "directions_walk", "hike": "hiking",
		"ride": "directions_bike", "swim": "pool", "row": "rowing",
		"ski": "downhill_skiing", "workout": "fitness_center", "other": "sports",
	}
	if !reflect.DeepEqual(fitnessMaterialNames, wantNames) || len(icons) != 9 {
		t.Fatal("incorrect sport-to-official-icon mapping")
	}
	// Pin the unmodified official files, independently of the decoder. Source:
	// google/material-design-icons at 49d4db35df873165d6bd6ba09b063c7dafbac2f4.
	wantHashes := map[string]string{
		"directions_bike": "d9430f8604873ebb9e38e9ff75b2c3df31969d14fd8453f54fe5d5392e4756e4",
		"directions_run":  "9a37f96e26b4cfca8bed95133df8bb0f960f6d5caa4a609c84a0729b0b0a00c5",
		"directions_walk": "5a04da77b7b710a3fac566cc633c5224bba1ff292f04dd47f9fea9cf0094af5a",
		"downhill_skiing": "5615c086a4d0373f95acb52381c76734c1b28928b4db4aead3f49eb2d7ce14a5",
		"fitness_center":  "d4b3bd83b66eb9ffe2fa24015dde68770c492b0bdc26fa9f9753c3377744f4bc",
		"hiking":          "6a87895c5a908454ee3197cf5429d17619e3f0de0e08e5ed44314193a4132ab4",
		"pool":            "05c16c0dcfeedeec4d56e289938a9138237b8fd3fd6bcd75647fe2381afb6dfc",
		"rowing":          "8cffc8311a62f9928b42f45dc042405ed10eebb7645306919808abbdfdc64883",
		"sports":          "e2e2cb04d745dfbd5ce667da6f39501c8ca65c8c7479327f366eec7256989846",
	}
	for sport, name := range wantNames {
		t.Run(sport, func(t *testing.T) {
			data, err := materialIconFiles.ReadFile("materialicons/" + name + ".svg")
			if err != nil {
				t.Fatal(err)
			}
			if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != wantHashes[name] {
				t.Fatalf("upstream %s.svg bytes changed: %s", name, got)
			}
			parsed, err := parseFitnessMaterialSVG(data)
			if err != nil || !reflect.DeepEqual(parsed, icons[sport]) {
				t.Fatalf("embedded asset did not parse identically: %v", err)
			}
			wantShapes := 1
			if sport == "swim" || sport == "other" {
				wantShapes = 2 // Official <circle>, not a missing head/center.
			}
			if len(parsed.shapes) != wantShapes {
				t.Fatalf("shape count = %d, want %d", len(parsed.shapes), wantShapes)
			}
			for _, shape := range parsed.shapes {
				if len(shape.path) == 0 && shape.circle[2] == 0 {
					t.Fatal("empty shape")
				}
				for _, op := range shape.path {
					if !strings.ContainsRune("MLCZ", rune(op.kind)) {
						t.Fatalf("non-normalized command %c", op.kind)
					}
					for _, v := range op.args {
						if math.IsNaN(v) || math.IsInf(v, 0) || v < -.1 || v > 24.1 {
							t.Fatalf("coordinate outside official 24px viewBox: %v", v)
						}
					}
				}
			}
			cached, err := fitnessMaterialIcons()
			if err != nil || cached[sport] != icons[sport] {
				t.Fatal("parsed icon not cached immutably")
			}
		})
	}
}

func materialTestOp(kind byte, args ...float64) fitnessMaterialOp {
	op := fitnessMaterialOp{kind: kind}
	copy(op.args[:], args)
	return op
}

func TestFitnessMaterialPath(t *testing.T) {
	t.Parallel()
	m, l, c, z := byte('M'), byte('L'), byte('C'), byte('Z')
	for _, tc := range []struct {
		name, d string
		want    []fitnessMaterialOp
	}{
		{"implicit_move_lines", "M1 2 3 4 5 6", []fitnessMaterialOp{
			materialTestOp(m, 1, 2), materialTestOp(l, 3, 4), materialTestOp(l, 5, 6)}},
		{"relative_move_lines", "m1 2 3 4 -2 -1", []fitnessMaterialOp{
			materialTestOp(m, 1, 2), materialTestOp(l, 4, 6), materialTestOp(l, 2, 5)}},
		{"decimal_signs", "M.9-.8l+.1.8", []fitnessMaterialOp{
			materialTestOp(m, .9, -.8), materialTestOp(l, 1, 0)}},
		{"horizontal_vertical_exponents", "M1e0 -2E+0H3v4h-1V.5", []fitnessMaterialOp{
			materialTestOp(m, 1, -2), materialTestOp(l, 3, -2), materialTestOp(l, 3, 2), materialTestOp(l, 2, 2), materialTestOp(l, 2, .5)}},
		{"implicit_axis_repeats", "M1 2H3 4v5 6", []fitnessMaterialOp{
			materialTestOp(m, 1, 2), materialTestOp(l, 3, 2), materialTestOp(l, 4, 2), materialTestOp(l, 4, 7), materialTestOp(l, 4, 13)}},
		{"relative_cubic_repeats", "M0 0c1 2 3 4 5 6 1 2 3 4 5 6", []fitnessMaterialOp{
			materialTestOp(m, 0, 0), materialTestOp(c, 1, 2, 3, 4, 5, 6), materialTestOp(c, 6, 8, 8, 10, 10, 12)}},
		{"smooth_cubic_reflection", "M1 2C3 4 5 6 7 8S9 10 11 12s1 2 3 4", []fitnessMaterialOp{
			materialTestOp(m, 1, 2), materialTestOp(c, 3, 4, 5, 6, 7, 8), materialTestOp(c, 9, 10, 9, 10, 11, 12), materialTestOp(c, 13, 14, 12, 14, 14, 16)}},
		{"smooth_repeats", "M0 0S1 2 3 4 5 6 7 8", []fitnessMaterialOp{
			materialTestOp(m, 0, 0), materialTestOp(c, 0, 0, 1, 2, 3, 4), materialTestOp(c, 5, 6, 5, 6, 7, 8)}},
		{"smooth_reset_after_line", "M1 2C3 4 5 6 7 8L9 10S11 12 13 14", []fitnessMaterialOp{
			materialTestOp(m, 1, 2), materialTestOp(c, 3, 4, 5, 6, 7, 8), materialTestOp(l, 9, 10), materialTestOp(c, 9, 10, 11, 12, 13, 14)}},
		{"smooth_reset_after_close", "M1 2C3 4 5 6 7 8zs2 3 4 5", []fitnessMaterialOp{
			materialTestOp(m, 1, 2), materialTestOp(c, 3, 4, 5, 6, 7, 8), materialTestOp(z), materialTestOp(c, 1, 2, 3, 5, 5, 7)}},
		{"close_and_relative_subpath", "M1,2l3,4zm2,3 1,1Z", []fitnessMaterialOp{
			materialTestOp(m, 1, 2), materialTestOp(l, 4, 6), materialTestOp(z), materialTestOp(m, 3, 5), materialTestOp(l, 4, 6), materialTestOp(z)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseFitnessMaterialPath(tc.d)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("decode %q = %#v, %v; want %#v", tc.d, got, err, tc.want)
			}
		})
	}
}

func TestFitnessMaterialPathRejectsUnsupported(t *testing.T) {
	t.Parallel()
	for _, d := range []string{
		"", "L1 2", "Z", "M", "M1", "M0 0L", "M0 0C1 2 3 4 5", "M0 0S1 2 3",
		"M0 0Q1 2 3 4", "M0 0A1 1 0 0 0 2 2", "M0 0T1 2", "M0 0R1 2",
		"M0 0junk", "M0 0!", "MNaN 0", "M0 Inf", "M1e 0", "M1e999 0",
		"M1000001 0", "M1000000 0l1 0", "M,0 0", "M0,,0", "M0 0,", "M0 0,Z", "M0 0Z1 2",
	} {
		t.Run(d, func(t *testing.T) {
			if got, err := parseFitnessMaterialPath(d); err == nil {
				t.Fatalf("accepted unsupported/junk path %q: %#v", d, got)
			}
		})
	}
}

func materialTestSVG(body string) []byte {
	return []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24">` + body + `</svg>`)
}

func TestFitnessMaterialSVGSubset(t *testing.T) {
	t.Parallel()
	good := materialTestSVG(`<rect fill="none" width="24" height="24"/><path fill="none" d="M0 0h24v24z"/><g><path d="M2 2h6v6h-6z"/></g><circle cx="16" cy="16" r="2"/>`)
	icon, err := parseFitnessMaterialSVG(good)
	if err != nil || len(icon.shapes) != 2 {
		t.Fatalf("background rectangles/paths must be ignored: %v", err)
	}
	for _, body := range []string{
		``, `<path d="M0 0Q1 2 3 4"/>`, `<path d="M0 0L1 1" transform="rotate(5)"/>`,
		`<path d="M0 0L1 1" stroke="red"/>`, `<path d="M0 0L1 1" fill="red"/>`,
		`<g transform="scale(2)"><path d="M0 0L1 1"/></g>`, `<use href="https://example.invalid/icon.svg"/>`,
		`<rect width="24" height="24"/>`, `<circle cx="1" cy="2" r="0"/>`, `<circle cx="NaN" cy="2" r="1"/>`,
		`<circle cx="1" cy="2" r="Inf"/>`, `<circle cx="1" cy="2"/>`, `<ellipse cx="1" cy="2" rx="1" ry="2"/>`,
		`<path d="M0 0L1 1"><path d="M0 0L1 1"/></path>`, `<svg><path d="M0 0L1 1"/></svg>`,
		`<path d="M0 0L1 1">text</path>`, `<path d="M0 0L1 1"`,
	} {
		if _, err := parseFitnessMaterialSVG(materialTestSVG(body)); err == nil {
			t.Errorf("accepted unsupported SVG: %s", body)
		}
	}
	for _, bad := range [][]byte{
		bytes.Replace(good, []byte(`viewBox="0 0 24 24"`), []byte(`viewBox="0 0 48 48"`), 1),
		bytes.Replace(good, []byte(`width="24"`), []byte(`width="48"`), 1),
		bytes.Replace(good, []byte(`height="24"`), []byte(`height="48"`), 1),
		append(append([]byte{}, good...), good...),
		append([]byte(`<!DOCTYPE svg [<!ENTITY e SYSTEM "http://127.0.0.1/private">]>`), good...),
	} {
		if _, err := parseFitnessMaterialSVG(bad); err == nil {
			t.Error("accepted invalid root, dimensions, or directive")
		}
	}
}

func TestFitnessMaterialFillClosure(t *testing.T) {
	t.Parallel()
	// Unclosed subpaths must fill exactly like explicitly closed SVG subpaths;
	// the first subpath must not acquire a bridge to the second.
	open, err := parseFitnessMaterialSVG(materialTestSVG(`<path d="M2 2h6v6h-6 M16 16h6v6h-6"/>`))
	if err != nil {
		t.Fatal(err)
	}
	closed, err := parseFitnessMaterialSVG(materialTestSVG(`<path d="M2 2h6v6h-6z M16 16h6v6h-6z"/>`))
	if err != nil {
		t.Fatal(err)
	}
	images := []*image.RGBA{image.NewRGBA(image.Rect(0, 0, 48, 48)), image.NewRGBA(image.Rect(0, 0, 48, 48))}
	for i, icon := range []*fitnessMaterialIcon{open, closed} {
		icon.draw(images[i], fitnessPen{vector.NewRasterizer(48, 48)}, 24, 24, 64)
	}
	if !bytes.Equal(images[0].Pix, images[1].Pix) || images[0].RGBAAt(24, 24).A != 0 || images[0].RGBAAt(10, 10).A != 255 || images[0].RGBAAt(38, 38).A != 255 {
		t.Fatal("SVG fill closure or separate subpaths rendered incorrectly")
	}
}

func TestFitnessMaterialRunnerSilhouette(t *testing.T) {
	t.Parallel()
	img := image.NewRGBA(image.Rect(0, 0, 40, 40))
	drawFitnessMaterialIcon(img, fitnessPen{vector.NewRasterizer(40, 40)}, "run", 20, 20, 38)
	// Golden alpha silhouette at a real card badge size; no route or background.
	alpha := make([]byte, 40*40)
	partial := false
	for y := 0; y < 40; y++ {
		for x := 0; x < 40; x++ {
			a := img.RGBAAt(x, y).A
			alpha[y*40+x] = a
			partial = partial || (a > 0 && a < 255)
		}
	}
	if !partial {
		t.Fatal("glyph edges are not antialiased at the actual badge size")
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(alpha)); got != "0366b705cbc5ae0bdf9520caa216f1f42e35c9be1c61125664ae9cfcfac4a0dc" {
		t.Fatalf("official runner alpha silhouette changed: %s", got)
	}
}

func TestFitnessMaterialBadgeIsolation(t *testing.T) {
	t.Parallel()
	segments, err := parseFitnessGPX(context.Background(), strings.NewReader(fitnessTestGPX))
	if err != nil {
		t.Fatal(err)
	}
	var reference *image.RGBA
	seen := make(map[[32]byte]string)
	for _, sport := range []string{"run", "walk", "hike", "ride", "swim", "row", "ski", "workout", "other"} {
		img, err := drawFitnessThumbnail(context.Background(), segments, sport, 320, 180)
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(buf.Bytes())
		if old, ok := seen[hash]; ok {
			t.Fatalf("PNG glyphs for %s and %s are identical", old, sport)
		}
		seen[hash] = sport
		if fitnessRedPixels(img) < 80 {
			t.Fatal("red route disappeared")
		}
		if reference != nil {
			if fitnessRedPixels(img) != fitnessRedPixels(reference) {
				t.Fatal("sport changed the red route")
			}
			for y := 0; y < 180; y++ {
				for x := 0; x < 320; x++ {
					// The badge occupies only this bottom-left square.
					if (x >= 58 || y < 122) && img.RGBAAt(x, y) != reference.RGBAAt(x, y) {
						t.Fatalf("sport %s changed pixels outside the badge at %d,%d", sport, x, y)
					}
				}
			}
		}
		if sport == "other" {
			unknown, err := drawFitnessThumbnail(context.Background(), segments, "unknown", 320, 180)
			if err != nil || !bytes.Equal(img.Pix, unknown.Pix) {
				t.Fatal("unknown sport did not use the official sports icon")
			}
		}
		reference = img
	}
	if fitnessRenderVersion != 4 || !strings.HasSuffix(fitnessCacheKey("synthetic", "run", 320, 180), ":fv4") {
		t.Fatal("glyph pixels changed without invalidating the thumbnail cache")
	}
}

func TestFitnessMaterialLicense(t *testing.T) {
	if !strings.Contains(fitnessMaterialLicense, "Apache License") || !strings.Contains(fitnessMaterialLicense, "Version 2.0, January 2004") || len(fitnessMaterialLicense) < 10000 {
		t.Fatal("full upstream Apache 2.0 license not embedded")
	}
	for _, text := range legal.Licenses() {
		if text == "Google Material Design Icons\n\n"+fitnessMaterialLicense {
			return
		}
	}
	t.Fatal("embedded Material icon license not registered")
}
