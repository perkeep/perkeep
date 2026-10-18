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

package runkeeper

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/schema/nodeattr"
)

const repairRoot = `<gpx xmlns="` + gpxNamespace + `" version="1.1">`
const repairPointA = `<trkpt lat="0" lon="0"><time>2024-01-02T03:04:05-08:00</time></trkpt>`
const repairPointB = `<trkpt lat="1" lon="2"/>`

func TestRepairTrackPreservesSourceBytesAndSegments(t *testing.T) {
	if gpxRepairVersion != "1" || attrGPXRepairVersion != "runkeeperGPXRepairVersion" || attrGPXRepairCount != "runkeeperGPXRepairCount" {
		t.Fatal("repair version or provenance attributes differ from the stored schema")
	}
	const preamble = "<?xml version=\"1.0\"?>\r\n<!-- source comment -->\r\n"
	const extensions = `<extensions><x:data xmlns:x="https://example.invalid/ext" quoted="a &amp; b"><![CDATA[<trkseg> is not markup]]></x:data></extensions>`
	for _, tt := range []struct {
		name, original, repaired string
		count                    int
		segments                 []int
	}{
		{"emptyFirstSegment", malformedNestedGPX, strings.Replace(malformedNestedGPX, "<trkseg><trkseg>", "<trkseg></trkseg><trkseg>", 1), 1, []int{0, 2}},
		{"separatePointGroups", repairRoot + `<trk><trkseg>` + repairPointA + "\r\n<trkseg>" + repairPointB + `</trkseg></trk></gpx>`, repairRoot + `<trk><trkseg>` + repairPointA + "\r\n</trkseg><trkseg>" + repairPointB + `</trkseg></trk></gpx>`, 1, []int{1, 1}},
		{"multipleMissingCloses", repairRoot + `<trk><trkseg>` + repairPointA + `<trkseg>` + repairPointB + `<trkseg>` + repairPointB + `</trkseg></trk></gpx>`, repairRoot + `<trk><trkseg>` + repairPointA + `</trkseg><trkseg>` + repairPointB + `</trkseg><trkseg>` + repairPointB + `</trkseg></trk></gpx>`, 2, []int{1, 1, 1}},
		{"formatAndExtensions", preamble + repairRoot + `<trk><name>A &amp; B</name><trkseg>` + repairPointA + extensions + "\n  <trkseg arbitrary='preserved'>" + repairPointB + `</trkseg></trk></gpx><!-- tail -->`, preamble + repairRoot + `<trk><name>A &amp; B</name><trkseg>` + repairPointA + extensions + "\n  </trkseg><trkseg arbitrary='preserved'>" + repairPointB + `</trkseg></trk></gpx><!-- tail -->`, 1, []int{1, 1}},
		{"prefixedGPX", `<g:gpx xmlns:g="` + gpxNamespace + `" version="1.1"><g:trk><g:trkseg><g:trkpt lat="0" lon="0"/><g:trkseg><g:trkpt lat="1" lon="2"/></g:trkseg></g:trk></g:gpx>`, `<g:gpx xmlns:g="` + gpxNamespace + `" version="1.1"><g:trk><g:trkseg><g:trkpt lat="0" lon="0"/></g:trkseg><g:trkseg><g:trkpt lat="1" lon="2"/></g:trkseg></g:trk></g:gpx>`, 1, []int{1, 1}},
		{"differentPrefixes", `<g:gpx xmlns:g="` + gpxNamespace + `" xmlns:h="` + gpxNamespace + `" version="1.1"><g:trk><g:trkseg><g:trkpt lat="0" lon="0"/><h:trkseg><h:trkpt lat="1" lon="2"/></h:trkseg></g:trk></g:gpx>`, `<g:gpx xmlns:g="` + gpxNamespace + `" xmlns:h="` + gpxNamespace + `" version="1.1"><g:trk><g:trkseg><g:trkpt lat="0" lon="0"/></g:trkseg><h:trkseg><h:trkpt lat="1" lon="2"/></h:trkseg></g:trk></g:gpx>`, 1, []int{1, 1}},
		{"maximumDepth", repairRoot + `<trk><trkseg><extensions>` + strings.Repeat("<x>", 60) + strings.Repeat("</x>", 60) + `</extensions><trkseg>` + repairPointA + `</trkseg></trk></gpx>`, repairRoot + `<trk><trkseg><extensions>` + strings.Repeat("<x>", 60) + strings.Repeat("</x>", 60) + `</extensions></trkseg><trkseg>` + repairPointA + `</trkseg></trk></gpx>`, 1, []int{0, 1}},
		{"multipleTracks", repairRoot + `<trk><trkseg>` + repairPointB + `</trkseg></trk><trk><trkseg>` + repairPointA + `<trkseg>` + repairPointB + `</trkseg></trk></gpx>`, repairRoot + `<trk><trkseg>` + repairPointB + `</trkseg></trk><trk><trkseg>` + repairPointA + `</trkseg><trkseg>` + repairPointB + `</trkseg></trk></gpx>`, 1, []int{1, 1, 1}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			count, err := repairTrack(strings.NewReader(tt.original), &out)
			if err != nil || count != tt.count {
				t.Fatalf("repair = %d, %v; want %d closures", count, err, tt.count)
			}
			if out.String() != tt.repaired {
				t.Fatalf("repair changed bytes beyond inserted segment closes:\n got %q\nwant %q", out.String(), tt.repaired)
			}
			info, err := parseTrack(strings.NewReader(out.String()), "original-hash")
			if err != nil || !info.Usable || info.Hash != "original-hash" {
				t.Fatalf("derived GPX not strictly usable: %+v, %v", info, err)
			}
			if got := trackSegmentPointCounts(t, out.String()); !slices.Equal(got, tt.segments) {
				t.Fatalf("segment point counts = %v, want %v; repair merged or dropped a segment", got, tt.segments)
			}
		})
	}
}

func trackSegmentPointCounts(t *testing.T, text string) []int {
	t.Helper()
	d := xml.NewDecoder(strings.NewReader(text))
	var counts []int
	for {
		tok, err := d.Token()
		if err == io.EOF {
			return counts
		}
		if err != nil {
			t.Fatal(err)
		}
		if start, ok := tok.(xml.StartElement); ok && start.Name.Space == gpxNamespace {
			switch start.Name.Local {
			case "trkseg":
				counts = append(counts, 0)
			case "trkpt":
				if len(counts) == 0 {
					t.Fatal("point outside segment")
				}
				counts[len(counts)-1]++
			}
		}
	}
}

func TestRepairTrackRejectsOtherXMLDefects(t *testing.T) {
	for name, text := range map[string]string{
		"validNoFixes":             goodGPX,
		"empty":                    "",
		"balancedNested":           balancedNestedGPX,
		"missingFinalSegmentClose": strings.Replace(goodGPX, "</trkseg>", "", 1),
		"unrelatedMismatch":        strings.Replace(malformedNestedGPX, "</trk>", "</other>", 1),
		"unclosedPoint":            strings.Replace(malformedNestedGPX, "</trkpt>", "", 1),
		"truncated":                strings.TrimSuffix(malformedNestedGPX, "</gpx>"),
		"trailingData":             malformedNestedGPX + "not XML",
		"leadingData":              "not XML" + malformedNestedGPX,
		"extraRoot":                malformedNestedGPX + repairRoot + "</gpx>",
		"wrongNamespace":           strings.Replace(malformedNestedGPX, gpxNamespace, "https://example.invalid/gpx", 1),
		"missingNamespace":         strings.Replace(malformedNestedGPX, ` xmlns="`+gpxNamespace+`"`, "", 1),
		"wrongVersion":             strings.Replace(malformedNestedGPX, `version="1.1"`, `version="1.0"`, 1),
		"namespacedVersion":        strings.Replace(malformedNestedGPX, `version="1.1"`, `xmlns:x="https://example.invalid" x:version="1.1"`, 1),
		"foreignNewSegment":        strings.Replace(malformedNestedGPX, "<trkseg><trkseg>", `<trkseg><trkseg xmlns="https://example.invalid">`, 1),
		"undeclaredPrefix":         strings.Replace(malformedNestedGPX, "<trkseg><trkseg>", "<trkseg><x:trkseg>", 1),
		"segmentNamespaceBindings": strings.Replace(malformedNestedGPX, "<trkseg>", `<trkseg xmlns:x="https://example.invalid">`, 1),
		"segmentUnderExtension":    repairRoot + `<trk><trkseg><extensions><trkseg>` + repairPointA + `</trkseg></extensions></trkseg></trk></gpx>`,
		"segmentInsidePoint":       repairRoot + `<trk><trkseg><trkpt lat="0" lon="0"><trkseg>` + repairPointB + `</trkseg></trkpt></trkseg></trk></gpx>`,
		"excessiveDepth":           repairRoot + `<trk><trkseg><extensions>` + strings.Repeat("<x>", 64) + strings.Repeat("</x>", 64) + `</extensions><trkseg>` + repairPointA + `</trkseg></trk></gpx>`,
	} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			if count, err := repairTrack(strings.NewReader(text), &out); !errors.Is(err, errGPXRepairRejected) {
				t.Fatalf("unrelated or ambiguous XML defect returned %d, %v; want rejected-repair sentinel", count, err)
			}
		})
	}
}

func TestInspectRepairedTrackStrictValidation(t *testing.T) {
	for name, text := range map[string]string{
		"badCoordinates":       strings.Replace(malformedNestedGPX, `lat="0"`, `lat="NaN"`, 1),
		"outOfRangeCoordinate": strings.Replace(malformedNestedGPX, `lon="2"`, `lon="181"`, 1),
		"invalidTime":          strings.Replace(malformedNestedGPX, "2024-01-02", "2024-02-30", 1),
		"unzonedTime":          strings.Replace(malformedNestedGPX, "-08:00", "", 1),
		"longTime":             strings.Replace(malformedNestedGPX, "2024-01-02T03:04:05-08:00", strings.Repeat("x", 129), 1),
		"tooManyPoints":        strings.Replace(malformedNestedGPX, "</trkseg>", strings.Repeat(repairPointB, maxRenderPoints)+"</trkseg>", 1),
	} {
		t.Run(name, func(t *testing.T) {
			info, err := inspectTrack(source(t, map[string]string{"track.gpx": text}), "track.gpx")
			if err != nil || info.Usable || info.Warning == "" || info.Hash == "" || info.Latitude != "" || info.Longitude != "" || info.RepairVersion != "" || info.RepairCount != 0 {
				t.Fatalf("XML repair bypassed strict route validation: %+v, %v", info, err)
			}
		})
	}
	// The source parser finds this track-level instant before rejecting nested
	// segments; repair must not replace it with the later point time.
	original := strings.Replace(malformedNestedGPX, "<trk>", "<trk><time>2023-12-31T01:00:00+01:00</time>", 1)
	info, err := inspectTrack(source(t, map[string]string{"track.gpx": original}), "track.gpx")
	hash := sha256.Sum256([]byte(original))
	if err != nil || !info.Usable || info.Start != "2023-12-31T00:00:00Z" || info.Latitude != "0" || info.Longitude != "0" || info.RepairVersion != "1" || info.RepairCount != 1 || info.Hash != hex.EncodeToString(hash[:]) || !strings.Contains(strings.ToLower(info.Warning), "repaired") {
		t.Fatalf("repaired inspection lost source date/hash or repair provenance: %+v, %v", info, err)
	}
}

// repeatReader generates large synthetic XML without a member-sized allocation.
type repairRepeatReader struct {
	chunk  string
	left   int
	offset int
}

func (r *repairRepeatReader) Read(p []byte) (int, error) {
	if r.left == 0 {
		return 0, io.EOF
	}
	n := min(len(p), r.left)
	for i := 0; i < n; i++ {
		p[i] = r.chunk[r.offset%len(r.chunk)]
		r.offset++
	}
	r.left -= n
	return n, nil
}

type repairCountWriter struct{ n int }

func (w *repairCountWriter) Write(p []byte) (int, error) { w.n += len(p); return len(p), nil }

func TestRepairTrackByteBounds(t *testing.T) {
	prefix := repairRoot + `<trk><trkseg><!--`
	suffix := `--><trkseg>` + repairPointA + `</trkseg></trk></gpx>`
	for _, tt := range []struct {
		name    string
		padding int
		bad     bool
	}{
		{"exactOutputBudget", maxRenderTrack - len(prefix) - len(suffix) - len("</trkseg>"), false},
		{"outputTooLarge", maxRenderTrack - len(prefix) - len(suffix), true},
		{"inputTooLarge", maxRenderTrack, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			input := &countingReader{r: io.MultiReader(strings.NewReader(prefix), &repairRepeatReader{chunk: "x", left: tt.padding}, strings.NewReader(suffix))}
			var out repairCountWriter
			count, err := repairTrack(input, &out)
			if (err != nil) != tt.bad || !tt.bad && count != 1 {
				t.Fatalf("repair = %d, %v; bad=%v", count, err, tt.bad)
			}
			if tt.bad && !errors.Is(err, errGPXRepairRejected) {
				t.Fatalf("byte budget failure = %v, want rejected-repair sentinel", err)
			}
			if out.n > maxRenderTrack || input.n > maxRenderTrack+1 {
				t.Fatalf("repair exceeded byte budgets: read=%d written=%d", input.n, out.n)
			}
		})
	}
}

type repairStreamingWriter struct {
	input        *countingReader
	firstWriteAt int
}

func (w *repairStreamingWriter) Write(p []byte) (int, error) {
	if len(p) != 0 && w.firstWriteAt == 0 {
		w.firstWriteAt = w.input.n
	}
	return len(p), nil
}

func TestRepairTrackStreamsOutput(t *testing.T) {
	const comment = "<!-- synthetic padding -->"
	input := &countingReader{r: io.MultiReader(
		strings.NewReader(repairRoot+"<trk><trkseg>"+repairPointA),
		&repairRepeatReader{chunk: comment, left: len(comment) * 10000},
		strings.NewReader("<trkseg>"+repairPointB+"</trkseg></trk></gpx>"),
	)}
	out := &repairStreamingWriter{input: input}
	if count, err := repairTrack(input, out); err != nil || count != 1 {
		t.Fatalf("streaming repair = %d, %v", count, err)
	}
	if out.firstWriteAt == 0 || out.firstWriteAt > 64<<10 {
		t.Fatalf("repair buffered the member before producing output: first write after %d input bytes", out.firstWriteAt)
	}
}

type repairErrorReader struct{ err error }

func (r repairErrorReader) Read([]byte) (int, error) { return 0, r.err }

type repairErrorWriter struct{ err error }

func (w repairErrorWriter) Write([]byte) (int, error) { return 0, w.err }

type repairDataErrorReader struct {
	data []byte
	err  error
}

func (r *repairDataErrorReader) Read(p []byte) (int, error) {
	n := copy(p, r.data)
	r.data = r.data[n:]
	if len(r.data) == 0 {
		return n, r.err
	}
	return n, nil
}

func TestRepairTrackIOErrors(t *testing.T) {
	injected := errors.New("synthetic repair I/O failure")
	if _, err := repairTrack(io.MultiReader(strings.NewReader(malformedNestedGPX), repairErrorReader{injected}), io.Discard); !errors.Is(err, injected) {
		t.Fatalf("reader failure = %v, want injected failure", err)
	}
	if _, err := repairTrack(strings.NewReader(malformedNestedGPX), repairErrorWriter{injected}); !errors.Is(err, injected) {
		t.Fatalf("writer failure = %v, want injected failure", err)
	}
	// Readers may return useful bytes and an error together. Don't mistake an
	// error in a buffered read for an ordinary XML/repair rejection.
	for _, text := range []string{malformedNestedGPX, balancedNestedGPX, "<broken"} {
		r := &repairDataErrorReader{data: []byte(text), err: injected}
		if _, err := repairTrack(r, io.Discard); !errors.Is(err, injected) {
			t.Fatalf("data-and-error reader failure = %v, want injected failure", err)
		}
	}
}

func TestRepairLateUnsupportedXMLFallsBack(t *testing.T) {
	for name, declaration := range map[string]string{
		"version":  `<?xml version="1.1"?>`,
		"encoding": `<?xml version="1.0" encoding="ISO-8859-1"?>`,
	} {
		t.Run(name, func(t *testing.T) {
			// Strict parsing encounters the known nested-segment defect first.
			// A decoder error later in the repair pass is still malformed source
			// XML, not a source-reader I/O failure that should abort the import.
			original := strings.Replace(malformedNestedGPX, "<trk>", "<trk><time>2024-01-02T03:04:05-08:00</time>", 1)
			original = strings.Replace(original, "<trkseg><trkseg>", "<trkseg><trkseg>"+declaration, 1)
			var out bytes.Buffer
			if count, err := repairTrack(strings.NewReader(original), &out); !errors.Is(err, errGPXRepairRejected) {
				t.Fatalf("unsupported XML declaration returned %d, %v; want rejected-repair sentinel", count, err)
			}
			src := source(t, map[string]string{"track.gpx": original})
			info, err := inspectTrack(src, "track.gpx")
			if err != nil || info.Usable || info.Warning == "" || info.Hash == "" || info.Start != "2024-01-02T11:04:05Z" || info.Latitude != "" || info.Longitude != "" || info.RepairVersion != "" || info.RepairCount != 0 {
				t.Fatalf("unsupported XML declaration did not retain metadata-only fallback: %+v, %v", info, err)
			}
			f := newFixture(t)
			p := f.run(t, source(t, repairedArchiveFiles(original)))
			a := f.activity(t, idA)
			if p.Done != 2 || p.WarningCount != 2 || a.Attr(attrVersion) != importVersion || a.Attr(nodeattr.Description) != "Notes" || a.Attr("startDate") != "2024-01-02T11:04:05Z" || a.Attr(attrStartDateSource) != startDateSourceGPX {
				t.Fatalf("malformed repair candidate aborted metadata import or lost original date: %+v", p)
			}
			if a.Attr(nodeattr.FitnessTrack) != "" || a.Attr(attrGPXRepairVersion) != "" || a.Attr(attrGPXRepairCount) != "" || a.Attr("latitude") != "" || a.Attr("longitude") != "" {
				t.Fatal("unsupported XML declaration published a repaired route or repair markers")
			}
			if f.file(t, a.Attr(attrSourceGPX)) != original {
				t.Fatal("unsupported XML declaration caused original source loss")
			}
			before := f.s.NumBlobs()
			p = f.run(t, source(t, repairedArchiveFiles(original)))
			if p.Unchanged != 2 || f.s.NumBlobs() != before {
				t.Fatal("metadata-only XML fallback churned on repeat")
			}
		})
	}
}

// Run manually with -run '^$' -bench BenchmarkRepairTrackRootAttributes.
// The point count is fixed: unrelated root attributes must not be rescanned
// for every point during namespace resolution. There is no timing assertion.
func BenchmarkRepairTrackRootAttributes(b *testing.B) {
	for _, attributes := range []int{2000, 20000} {
		b.Run(strconv.Itoa(attributes), func(b *testing.B) {
			var original strings.Builder
			original.WriteString(`<gpx xmlns="` + gpxNamespace + `" version="1.1"`)
			for i := 0; i < attributes; i++ {
				original.WriteString(` a` + strconv.Itoa(i) + `="preserved"`)
			}
			original.WriteString(`><trk><trkseg>`)
			original.WriteString(strings.Repeat(repairPointB, 1000))
			original.WriteString(`<trkseg>`)
			original.WriteString(strings.Repeat(repairPointB, 1000))
			original.WriteString(`</trkseg></trk></gpx>`)
			text := original.String()
			b.SetBytes(int64(len(text)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if count, err := repairTrack(strings.NewReader(text), io.Discard); err != nil || count != 1 {
					b.Fatalf("repair = %d, %v", count, err)
				}
			}
		})
	}
}

func repairedArchiveFiles(track string) map[string]string {
	files := synthetic()
	files["shared.gpx"] = track
	return files
}

func TestArchiveRepairsSeparateDerivedGPX(t *testing.T) {
	f := newFixture(t)
	files := repairedArchiveFiles(malformedNestedGPX)
	src := source(t, files)
	p := f.run(t, src)
	if p.Done != 2 || p.Unchanged != 0 || p.WarningCount != 2 {
		t.Fatalf("repair progress = %+v", p)
	}
	a, b := f.activity(t, idA), f.activity(t, idB)
	if a.PermanodeRef() == b.PermanodeRef() || a.Attr(nodeattr.FitnessTrack) != b.Attr(nodeattr.FitnessTrack) {
		t.Fatal("shared repair collapsed activity UUIDs or produced inconsistent derived refs")
	}
	for _, id := range []string{idA, idB} {
		a := f.activity(t, id)
		if a.Attr(nodeattr.FitnessTrack) == "" || a.Attr(nodeattr.FitnessTrack) == a.Attr(attrSourceGPX) || a.Attr(attrGPXRepairVersion) != "1" || a.Attr(attrGPXRepairCount) != "1" || a.Attr(attrVersion) != "3" || a.Attr(attrHash) == "" {
			t.Fatal("repaired activity lacks separate derived GPX or atomic completion/provenance")
		}
		if a.Attr("startDate") != "2024-01-02T11:04:05Z" || a.Attr(attrStartDateSource) != startDateSourceGPX || a.Attr("latitude") != "0" || a.Attr("longitude") != "0" {
			t.Fatal("repaired route lost date or start coordinates")
		}
		if f.file(t, a.Attr(attrSourceGPX)) != malformedNestedGPX {
			t.Fatal("repair overwrote original attachment")
		}
		derived := f.file(t, a.Attr(nodeattr.FitnessTrack))
		want := strings.Replace(malformedNestedGPX, "<trkseg><trkseg>", "<trkseg></trkseg><trkseg>", 1)
		if derived != want {
			t.Fatal("derived attachment differs beyond the required closing tag")
		}
		ref, _ := blob.Parse(a.Attr(nodeattr.FitnessTrack))
		r, err := schema.NewFileReader(f.rc.Context(), f.s, ref)
		if err != nil {
			t.Fatal(err)
		}
		if r.FileName() != "runkeeper-repaired-v1.gpx" {
			t.Errorf("derived filename=%q", r.FileName())
		}
		r.Close()
	}
	for _, warning := range p.Warnings {
		if !strings.Contains(strings.ToLower(warning), "repaired") {
			t.Fatal("successful repair warning is not explicit")
		}
	}
	before := f.s.NumBlobs()
	p = f.run(t, src)
	if p.Unchanged != 2 || f.s.NumBlobs() != before {
		t.Fatal("repeat repaired import added claims or attachment blobs")
	}
}

func TestRepairTemporaryFileCleanup(t *testing.T) {
	// Keep every temporary repair file visible to the test; the durable retry
	// input is the archive, never a leftover file in this directory.
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	t.Setenv("TMP", dir)
	t.Setenv("TEMP", dir)
	for name, original := range map[string]string{
		"repaired":          malformedNestedGPX,
		"balanced":          balancedNestedGPX,
		"badDerivedPoint":   strings.Replace(malformedNestedGPX, `lat="0"`, `lat="NaN"`, 1),
		"unrelatedBadXML":   strings.Replace(malformedNestedGPX, "</trk>", "</wrong>", 1),
		"excessiveTimeText": strings.Replace(malformedNestedGPX, "2024-01-02T03:04:05-08:00", strings.Repeat("x", 129), 1),
	} {
		t.Run(name, func(t *testing.T) {
			f, info, err := repairedTrackFile(source(t, map[string]string{"track.gpx": original}), "track.gpx", "hash")
			if name == "repaired" {
				if err != nil || f == nil || !info.Usable {
					t.Fatalf("repair file = %v, %+v, %v", f, info, err)
				}
				stat, err := f.Stat()
				if err != nil {
					t.Fatal(err)
				}
				if runtime.GOOS != "windows" && stat.Mode().Perm() != 0600 {
					t.Fatal("derived temporary GPX is not private")
				}
				// A returned successful file is rewound for streaming upload.
				text, err := io.ReadAll(f)
				if err != nil || string(text) != strings.Replace(malformedNestedGPX, "<trkseg><trkseg>", "<trkseg></trkseg><trkseg>", 1) {
					t.Fatal("successful repair temporary file was not rewound")
				}
				removeTrackFile(f)
			} else if err == nil || f != nil {
				if f != nil {
					removeTrackFile(f)
				}
				t.Fatal("invalid derived route returned a usable temporary file")
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 0 {
				t.Fatalf("repair leaked temporary files: %d entries, %v", len(entries), err)
			}
		})
	}
}

func TestRepairProvenanceRemoval(t *testing.T) {
	for _, target := range []string{"valid", "unrepairable", "noGPX"} {
		t.Run(target, func(t *testing.T) {
			f := newFixture(t)
			files := repairedArchiveFiles(malformedNestedGPX)
			f.run(t, source(t, files))
			a := f.activity(t, idA)
			ref := a.PermanodeRef()
			switch target {
			case "valid":
				files["shared.gpx"] = goodGPX
			case "unrepairable":
				files["shared.gpx"] = balancedNestedGPX
			case "noGPX":
				files["cardioActivities.csv"] = strings.ReplaceAll(files["cardioActivities.csv"], "shared.gpx", "")
				delete(files, "shared.gpx")
			}
			p := f.run(t, source(t, files))
			a = f.activity(t, idA)
			if a.PermanodeRef() != ref || a.Attr(attrGPXRepairVersion) != "" || a.Attr(attrGPXRepairCount) != "" || p.Unchanged != 0 {
				t.Fatal("refresh retained stale repair provenance or changed activity identity")
			}
			if target == "valid" {
				if a.Attr(nodeattr.FitnessTrack) != a.Attr(attrSourceGPX) || f.file(t, a.Attr(attrSourceGPX)) != goodGPX {
					t.Fatal("valid replacement must use its original directly")
				}
			} else if a.Attr(nodeattr.FitnessTrack) != "" || a.Attr("latitude") != "" || a.Attr("longitude") != "" {
				t.Fatal("replacement retained stale derived route or coordinates")
			}
			if target == "noGPX" && (a.Attr(attrSourceGPX) != "" || a.Attr("startDate") != "2024-01-02T03:04:05Z" || a.Attr(attrStartDateSource) != startDateSourceCSVAssumedUTC || p.WarningCount != 2) {
				t.Fatal("GPX removal changed the existing marked CSV date fallback policy")
			}
			before := f.s.NumBlobs()
			p = f.run(t, source(t, files))
			if p.Unchanged != 2 || f.s.NumBlobs() != before {
				t.Fatal("repair removal repeat churned claims or attachments")
			}
		})
	}
}

func TestVersionTwoRepairMigration(t *testing.T) {
	f := newFixture(t)
	src := source(t, repairedArchiveFiles(malformedNestedGPX))
	f.run(t, src)
	a := f.activity(t, idA)
	ref, original, activityJSON := a.PermanodeRef(), a.Attr(attrSourceGPX), a.Attr(attrActivityJSON)
	if err := a.SetAttrsWithDeletes([]string{nodeattr.FitnessTrack, "latitude", "longitude", attrGPXRepairVersion, attrGPXRepairCount}, attrVersion, "2"); err != nil {
		t.Fatal(err)
	}
	p := f.run(t, src)
	a = f.activity(t, idA)
	if p.Unchanged != 1 || a.PermanodeRef() != ref || a.Attr(attrVersion) != "3" || a.Attr(attrGPXRepairVersion) != "1" || a.Attr(attrGPXRepairCount) != "1" || a.Attr(nodeattr.FitnessTrack) == "" {
		t.Fatal("version-2 activity not repaired on the same UUID permanode")
	}
	if a.Attr(attrSourceGPX) != original || a.Attr(attrActivityJSON) != activityJSON {
		t.Fatal("repair migration changed original attachment refs")
	}
	before := f.s.NumBlobs()
	p = f.run(t, src)
	if p.Unchanged != 2 || f.s.NumBlobs() != before {
		t.Fatal("migrated repair repeat churned")
	}
}

func TestFailedRepairAtomicRetry(t *testing.T) {
	for _, initial := range []string{"new", "valid", "repaired"} {
		stages := []string{"derived", attrGPXRepairCount, attrHash}
		// SetAttrsWithDeletes deliberately omits unchanged values from claims.
		// Only inject failure for keys that this transition actually changes.
		if initial != "repaired" {
			stages = append(stages, attrGPXRepairVersion)
		}
		if initial == "new" {
			stages = append(stages, attrVersion)
		}
		for _, stage := range stages {
			t.Run(initial+"/"+stage, func(t *testing.T) {
				f := newFixture(t)
				files := repairedArchiveFiles(malformedNestedGPX)
				if initial != "new" {
					if initial == "valid" {
						files["shared.gpx"] = goodGPX
					}
					f.run(t, source(t, files))
				}
				a := f.activity(t, idA)
				ref := a.PermanodeRef()
				old := make(map[string]string)
				for _, key := range append(slices.Clone(activityKeys), attrHash, attrVersion) {
					old[key] = a.Attr(key)
				}
				// A changed repaired payload cannot hit an already stored schema.
				files["shared.gpx"] = strings.Replace(malformedNestedGPX, `lat="1"`, `lat="3"`, 1)
				files["shared.gpx"] = strings.Replace(files["shared.gpx"], "<trkseg><trkseg>", "<trkseg><trkseg><trkseg>", 1)
				files["cardioActivities.csv"] = strings.Replace(files["cardioActivities.csv"], ",Friend,Notes,", ",Friend,repair retry,", 1)
				if stage == "derived" {
					f.s.failNextFileUpload("runkeeper-repaired-v1.gpx")
				} else {
					f.s.failNextClaim(stage)
				}
				src := source(t, files)
				if err := importArchive(f.rc, src, src.names, nil); err == nil {
					t.Fatal("expected injected derived-upload or atomic-claim failure")
				}
				a = f.activity(t, idA)
				for key, want := range old {
					if got := a.Attr(key); got != want {
						t.Errorf("failed repair modified %s: got %q, want %q", key, got, want)
					}
				}
				f.run(t, src)
				a = f.activity(t, idA)
				if a.PermanodeRef() != ref || a.Attr(attrVersion) != "3" || a.Attr(attrGPXRepairVersion) != "1" || a.Attr(attrGPXRepairCount) != "2" || a.Attr(attrHash) == old[attrHash] || a.Attr(nodeattr.Description) != "repair retry" || a.Attr(nodeattr.FitnessTrack) == a.Attr(attrSourceGPX) {
					t.Fatal("retry did not publish the complete repaired activity atomically")
				}
				before := f.s.NumBlobs()
				p := f.run(t, src)
				if p.Unchanged != 2 || f.s.NumBlobs() != before {
					t.Fatal("successful repair retry is not a no-op on repeat")
				}
			})
		}
	}
}

func TestRepairCountMultipleInsertions(t *testing.T) {
	original := strings.Replace(malformedNestedGPX, "<trkseg><trkseg>", "<trkseg><trkseg><trkseg>", 1)
	f := newFixture(t)
	f.run(t, source(t, repairedArchiveFiles(original)))
	a := f.activity(t, idA)
	if count, err := strconv.Atoi(a.Attr(attrGPXRepairCount)); err != nil || count != 2 {
		t.Fatalf("repair insertion count=%q, want decimal 2", a.Attr(attrGPXRepairCount))
	}
}
