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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"perkeep.org/pkg/schema/nodeattr"
)

func TestParseActivity(t *testing.T) {
	for _, tt := range []struct {
		duration string
		want     float64
		bad      bool
	}{{"5:18", 318, false}, {"25:01:02", 90062, false}, {"100:00:00.25", 360000.25, false}, {"1:60", 0, true}, {"NaN:02", 0, true}, {"1.5:02", 0, true}, {"-1:00", 0, true}, {"12", 0, true}} {
		got, err := durationSeconds(tt.duration)
		if (err != nil) != tt.bad || !tt.bad && got != tt.want {
			t.Errorf("duration %q = %v,%v", tt.duration, got, err)
		}
	}
	a, err := parseActivity(rawRow{"Activity Id": idA, "Date": "2025-01-02 03:04:05", "Type": "Cycling", "Distance (km)": "2.5", "Climb (m)": "12.4", "Duration": "2:03", "Calories Burned": "0", "Average Heart Rate (bpm)": "0"})
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{nodeattr.FitnessDistance: "2500", nodeattr.FitnessElevationGain: "12.4", nodeattr.FitnessActivityType: "ride", nodeattr.FitnessEnergy: "0", nodeattr.FitnessAverageHeartRate: "0"} {
		if a.Attrs[k] != v {
			t.Errorf("%s=%q want %q", k, a.Attrs[k], v)
		}
	}
	if _, ok := a.Attrs["startDate"]; ok {
		t.Fatal("unzoned CSV date became startDate")
	}
	for _, row := range []rawRow{
		{"Activity Id": "../../unsafe"},
		{"Activity Id": idA, "Distance (mi)": "NaN"},
		{"Activity Id": idA, "Distance (mi)": "-1"},
		{"Activity Id": idA, "Distance (mi)": "1", "Distance (km)": "1"},
		{"Activity Id": idA, "Calories Burned": "1e308"},
	} {
		if _, err := parseActivity(row); err == nil {
			t.Errorf("accepted bad row %v", row)
		}
	}
	for source, want := range map[string]string{"Running": "run", "Walking": "walk", "Cycling": "ride", "Hiking": "hike", "Wheelchair": "other", "Other": "other", "unknown future type": "other"} {
		if got := activityType(source); got != want {
			t.Errorf("type %s=%s want %s", source, got, want)
		}
	}
}
func TestCSVBoundsAndColumns(t *testing.T) {
	for name, data := range map[string]string{
		"oversized":     `a,"` + strings.Repeat("x", maxRecord) + `"` + "\n",
		"quotedNewline": `a,"` + strings.Repeat("x\n", maxRecord/2) + `"` + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := newCSV(strings.NewReader(data)).read(); err == nil {
				t.Fatal("unbounded record accepted")
			}
		})
	}
	c := newCSV(strings.NewReader("\"a\nb\",\"c\"\"d\"\r\n"))
	row, err := c.read()
	if err != nil || len(row) != 2 || row[0] != "a\nb" || row[1] != `c"d` {
		t.Fatalf("quoted record: %q %v", row, err)
	}
	for _, data := range []string{"Other,Columns\n", "Activity Id, Activity Id\n", "Activity Id,Date,Type\n", "Activity Id,Date,Type,Duration\n" + idA + ",date,Running\n"} {
		src := source(t, map[string]string{"cardioActivities.csv": data})
		if err := readCSV(context.Background(), src, "cardioActivities.csv", maxMetadata, func(rawRow) error { return nil }); err == nil {
			t.Fatalf("invalid table accepted: %q", data)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	src := source(t, synthetic())
	err = readCSV(ctx, src, "cardioActivities.csv", maxMetadata, func(rawRow) error { return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
}
func TestProfileAndFilename(t *testing.T) {
	src := source(t, map[string]string{"profile.json": `{"username":" Optional[test] ","email":" Optional[ email@example.invalid ] ","name":" Name "}`})
	p, err := readProfile(src)
	if err != nil || p.UserName != "test" || p.Email != "email@example.invalid" || p.Name != "Name" {
		t.Fatalf("profile=%+v %v", p, err)
	}
	for _, data := range []string{`{"email":""}`, `{"username":"one","username":"two"}`, `{"username":1}`, `{"username":"one"} {}`, `[]`} {
		src := source(t, map[string]string{"profile.json": data})
		if _, err := readProfile(src); err == nil {
			t.Errorf("accepted invalid profile %s", data)
		}
	}
	for filename, want := range map[string]string{"runkeeper-data-export-1234-2026-10-15-141435.zip": "1234", "01-runkeeper-data-export-1234-2026-10-15-141435.zip": "1234", "renamed-1234.zip": ""} {
		m := filenameID.FindStringSubmatch(filename)
		got := ""
		if len(m) > 0 {
			got = m[1]
		}
		if got != want {
			t.Errorf("filename %s=%s want %s", filename, got, want)
		}
	}
}
func TestTrackValidation(t *testing.T) {
	for name, text := range map[string]string{
		"good":              goodGPX,
		"malformed":         strings.Replace(goodGPX, "</trkseg>", "", 1),
		"nested":            strings.Replace(goodGPX, `<trkpt lat="1" lon="2"/>`, `<trkseg><trkpt lat="1" lon="2"/></trkseg>`, 1),
		"missingTimezone":   strings.Replace(goodGPX, "-08:00", "", 1),
		"invalidCoordinate": strings.Replace(goodGPX, `lat="1"`, `lat="NaN"`, 1),
		"tooManyPoints":     `<gpx xmlns="` + gpxNamespace + `" version="1.1"><trk><trkseg>` + strings.Repeat(`<trkpt lat="1" lon="2"/>`, maxRenderPoints+1) + `</trkseg></trk></gpx>`,
		"tooManyBytes":      strings.Replace(goodGPX, "</gpx>", "<!--"+strings.Repeat("x", maxRenderTrack)+"--></gpx>", 1),
		"longTime":          strings.Replace(goodGPX, "2024-01-02T03:04:05-08:00", strings.Repeat("x", 129), 1),
	} {
		t.Run(name, func(t *testing.T) {
			src := source(t, map[string]string{"track.gpx": text})
			info, err := inspectTrack(src, "track.gpx")
			if err != nil {
				t.Fatal(err)
			}
			if info.Hash == "" {
				t.Fatal("missing complete source hash")
			}
			if name == "good" {
				if !info.Usable || info.Start != "2024-01-02T11:04:05Z" {
					t.Fatalf("good track rejected: %+v", info)
				}
			} else if info.Usable || info.Warning == "" {
				t.Fatal("unusable track accepted")
			}
		})
	}
	// Well-formed GPX without any point time remains renderable but has no instant.
	noTime := strings.Replace(goodGPX, "<time>2024-01-02T03:04:05-08:00</time>", "", 1)
	info, err := parseTrack(strings.NewReader(noTime), "hash")
	if err != nil || !info.Usable || info.Start != "" {
		t.Fatalf("timeless track=%+v %v", info, err)
	}
}

// generatedSensorReader never holds the generated CSV in memory. Cycling 64
// activity IDs forces writer eviction, bounding simultaneously open files.
type generatedSensorReader struct {
	left    int
	header  bool
	pending string
	maxHeap uint64
}

func (r *generatedSensorReader) Read(p []byte) (int, error) {
	if r.pending == "" {
		if !r.header {
			r.header = true
			r.pending = "Activity Id, Timestamp, Steps, Future\n"
		} else {
			if r.left == 0 {
				return 0, io.EOF
			}
			if r.left%50000 == 0 {
				runtime.GC()
				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				if m.HeapAlloc > r.maxHeap {
					r.maxHeap = m.HeapAlloc
				}
			}
			r.pending = fmt.Sprintf("%08x-1111-4111-8111-111111111111,%d,5,retained\n", r.left%64+1, r.left)
			r.left--
		}
	}
	n := copy(p, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}

type generatedSource struct{ reader *generatedSensorReader }

func (g generatedSource) Open(string) (io.ReadCloser, error) { return io.NopCloser(g.reader), nil }
func (g generatedSource) OpenLimit(string, int64) (io.ReadCloser, error) {
	return io.NopCloser(g.reader), nil
}
func TestLargeSensorMemberBoundedSpool(t *testing.T) {
	s, err := newSpool()
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	ids := make(map[string]bool)
	for i := 1; i <= 64; i++ {
		ids[fmt.Sprintf("%08x-1111-4111-8111-111111111111", i)] = true
	}
	reader := &generatedSensorReader{left: 250000}
	if err := s.read(context.Background(), generatedSource{reader}, "steps.csv", "steps", ids); err != nil {
		t.Fatal(err)
	}
	if len(s.open) > 32 || len(s.files) != 64 {
		t.Fatalf("unbounded writer/partition count: %d/%d", len(s.open), len(s.files))
	}
	if err := s.flush(); err != nil {
		t.Fatal(err)
	}
	if reader.maxHeap > before.HeapAlloc+24<<20 {
		t.Fatalf("live heap grew with sensor samples: %d -> %d", before.HeapAlloc, reader.maxHeap)
	}
	total := 0
	for _, name := range s.files {
		f, err := os.Open(name)
		if err != nil {
			t.Fatal(err)
		}
		var rowCount int
		// Count JSONL records without buffering the file.
		var b [32 << 10]byte
		for {
			n, e := f.Read(b[:])
			for _, v := range b[:n] {
				if v == '\n' {
					rowCount++
				}
			}
			if e == io.EOF {
				break
			}
			if e != nil {
				t.Fatal(e)
			}
		}
		f.Close()
		total += rowCount
	}
	if total != 250000 {
		t.Fatalf("spooled %d rows", total)
	}
	dir := s.dir
	s.close()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("temporary spool not removed")
	}
	t.Logf("streamed %s rows, peak sampled live heap delta %d bytes", strconv.Itoa(total), int64(reader.maxHeap)-int64(before.HeapAlloc))
}
func TestSpoolPathsPrivate(t *testing.T) {
	s, err := newSpool()
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	if err := s.add(idA+"-steps", rawRow{"Steps": "1"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(s.dir, idA+"-steps.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() {
		t.Fatal("sensor partition is not a regular file")
	}
	if err := s.flush(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(s.dir, idA+"-steps.jsonl"))
	if err != nil || string(data) != "{\"Steps\":\"1\"}\n" {
		t.Fatalf("sensor partition contents = %q, %v", data, err)
	}
	t.Run("UnixPermissions", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Windows uses ACLs, not Unix permission bits")
		}
		if info.Mode().Perm() != 0600 {
			t.Fatal("sensor file permissions not private")
		}
		dirInfo, err := os.Stat(s.dir)
		if err != nil {
			t.Fatal(err)
		}
		if dirInfo.Mode().Perm() != 0700 {
			t.Fatal("sensor directory permissions not private")
		}
	})
	t.Cleanup(func() {
		if _, err := os.Stat(s.dir); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("sensor directory remains after cleanup: %v", err)
		}
	})
}

type readerSource struct{ reader io.Reader }

func (s readerSource) Open(string) (io.ReadCloser, error) { return io.NopCloser(s.reader), nil }
func (s readerSource) OpenLimit(string, int64) (io.ReadCloser, error) {
	return io.NopCloser(s.reader), nil
}

type countingReader struct {
	r io.Reader
	n int
}

func (r *countingReader) Read(p []byte) (int, error) { n, err := r.r.Read(p); r.n += n; return n, err }
func TestHostileCSVHeadersRejectedBeforeRows(t *testing.T) {
	for _, header := range []string{
		"Activity Id," + strings.Repeat("X", maxRecord/2) + "\n",
		"Activity Id," + strings.Repeat("x,", maxColumns) + "last\n",
	} {
		t.Run(strconv.Itoa(len(header)), func(t *testing.T) {
			s, err := newSpool()
			if err != nil {
				t.Fatal(err)
			}
			defer s.close()
			tail := &generatedSensorReader{left: 1000000}
			counted := &countingReader{r: io.MultiReader(strings.NewReader(header), tail)}
			err = s.read(context.Background(), readerSource{counted}, "steps.csv", "steps", map[string]bool{idA: true})
			if err == nil {
				t.Fatal("oversized header accepted")
			}
			if len(s.files) != 0 || s.written != 0 || counted.n > len(header)+(32<<10) {
				t.Fatalf("processed hostile rows before rejecting: files=%d bytes=%d read=%d", len(s.files), s.written, counted.n)
			}
		})
	}
}
func TestEncodedSensorSpoolBudgets(t *testing.T) {
	row := rawRow{"Steps": "1", "Repeated header": "\x00\x00\x00\x00"}
	encoded, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	size := int64(len(encoded) + 1)
	for _, kind := range []string{"aggregate", "activity"} {
		t.Run(kind, func(t *testing.T) {
			s, err := newSpool()
			if err != nil {
				t.Fatal(err)
			}
			defer s.close()
			s.totalLimit = size * 3
			s.activityLimit = size * 3
			next := idB + "-steps"
			if kind == "aggregate" {
				s.totalLimit = size*2 - 1
			} else {
				s.activityLimit = size*2 - 1
				next = idA + "-heartRate"
			}
			if err := s.add(idA+"-steps", row); err != nil {
				t.Fatal(err)
			}
			if err := s.add(next, row); err == nil {
				t.Fatal("encoded output exceeded budget")
			}
			if s.written != size || len(s.files) != 1 {
				t.Fatalf("wrote rejected row: %d bytes %d files", s.written, len(s.files))
			}
			if err := s.flush(); err != nil {
				t.Fatal(err)
			}
			for _, name := range s.files {
				info, err := os.Stat(name)
				if err != nil {
					t.Fatal(err)
				}
				if info.Size() != size {
					t.Fatal("disk write exceeded budget")
				}
			}
		})
	}
	// Small rows with maximum-sized repeated headers must hit encoded byte limits,
	// not just the much smaller raw CSV length.
	header := "Activity Id," + strings.Repeat("h", maxColumnName) + "\n"
	input := header + strings.Repeat(idA+",x\n", 1000)
	s, err := newSpool()
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	s.totalLimit = 4096
	s.activityLimit = 4096
	err = s.read(context.Background(), readerSource{strings.NewReader(input)}, "steps.csv", "steps", map[string]bool{idA: true})
	if err == nil || s.written > 4096 {
		t.Fatal("header expansion bypassed spool budget")
	}
}
func TestEncodedPhotoManifestBudget(t *testing.T) {
	f := newFixture(t)
	files := synthetic()
	var header, row strings.Builder
	header.WriteString("Activity Id,Image File Name")
	row.WriteString(idA + ",image.jpg")
	for i := 0; i < 100; i++ {
		header.WriteString("," + fmt.Sprintf("%03d", i) + strings.Repeat("h", maxColumnName-3))
		row.WriteString(",x")
	}
	files["photos.csv"] = header.String() + "\n" + strings.Repeat(row.String()+"\n", 1000)
	src := source(t, files)
	before := f.s.NumBlobs()
	err := importArchive(f.rc, src, src.names, nil)
	if err == nil || !strings.Contains(err.Error(), "encoded photo metadata byte limit") {
		t.Fatalf("photo expansion accepted: %v", err)
	}
	if f.s.NumBlobs() != before {
		t.Fatal("published data before photo budget validation")
	}
}
func TestSpoolFlushClosesAllOnError(t *testing.T) {
	s, err := newSpool()
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	for _, id := range []string{idA, idB} {
		if err := s.add(id+"-steps", rawRow{"Steps": "1"}); err != nil {
			t.Fatal(err)
		}
	}
	// Closing one backing file makes its buffered Flush fail; all other handles
	// must still close so RemoveAll can succeed on platforms like Windows.
	first := s.open[idA+"-steps"].Value.(*spoolWriter)
	if err := first.f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.flush(); err == nil {
		t.Fatal("expected flush failure")
	}
	if len(s.open) != 0 || s.lru.Len() != 0 {
		t.Fatal("flush failure leaked remaining open handles")
	}
	s.close()
	if _, err := os.Stat(s.dir); !os.IsNotExist(err) {
		t.Fatal("failed spool cleanup")
	}
}
func TestWarningsBoundedInProvider(t *testing.T) {
	r := new(archiveRun)
	for i := 0; i < 200; i++ {
		r.warn(strings.Repeat("long", 10000))
	}
	if r.progress.WarningCount != 200 || len(r.progress.Warnings) != 100 {
		t.Fatal("warning count bound failed")
	}
	for _, w := range r.progress.Warnings {
		if len(w) > 1024 {
			t.Fatal("retained oversized warning")
		}
	}
}
