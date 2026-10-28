/*
Copyright 2026 The Perkeep Authors.

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

package quicktime

import (
	"bytes"
	"encoding/binary"
	"os"
	"reflect"
	"testing"
	"time"
)

// table returns a sample table FullBox body with a 32-bit entry count
// and the given 32-bit entry fields.
func table(count int, fields ...uint32) []byte {
	b := append(make([]byte, 4), u32(uint32(count))...)
	for _, f := range fields {
		b = append(b, u32(f)...)
	}
	return b
}

// testTables returns sample tables of 5 samples of sizes 10, 20, 30, 40,
// and 50 in two chunks, at offsets 1000 and 2000, of 2 and 3 samples,
// with durations 100, 100, 100, 200, and 200, and sync samples 1 and 4.
func testTables() *sampleTables {
	return &sampleTables{
		stts: table(2, 3, 100, 2, 200),
		stss: table(2, 1, 4),
		stsz: append(make([]byte, 4), append(u32(0), table(5, 10, 20, 30, 40, 50)[4:]...)...),
		stsc: table(2, 1, 2, 1, 2, 3, 1),
		stco: table(2, 1000, 2000),
	}
}

func TestSyncSamples(t *testing.T) {
	ms := time.Millisecond
	want := []Sample{{Time: 0, Offset: 1000, Size: 10}, {Time: 300 * ms, Offset: 2030, Size: 40}}
	all := []Sample{
		{0, 1000, 10}, {100 * ms, 1010, 20}, {200 * ms, 2000, 30}, {300 * ms, 2030, 40}, {500 * ms, 2070, 50},
	}
	stz2 := func(field byte, vals ...byte) []byte {
		return append([]byte{0, 0, 0, 0, 0, 0, 0, field, 0, 0, 0, 5}, vals...)
	}
	for _, tt := range []struct {
		name   string
		modify func(*sampleTables)
		want   []Sample
	}{
		{"stsz", func(*sampleTables) {}, want},
		{"no stss", func(st *sampleTables) { st.stss = nil }, all},
		{"co64", func(st *sampleTables) {
			st.stco = nil
			st.co64 = append(make([]byte, 4), append(u32(2), append(binary.BigEndian.AppendUint64(nil, 1000), binary.BigEndian.AppendUint64(nil, 2000)...)...)...)
		}, want},
		{"stz2 8", func(st *sampleTables) { st.stsz, st.stz2 = nil, stz2(8, 10, 20, 30, 40, 50) }, want},
		{"stz2 16", func(st *sampleTables) { st.stsz, st.stz2 = nil, stz2(16, 0, 10, 0, 20, 0, 30, 0, 40, 0, 50) }, want},
		{"stz2 4", func(st *sampleTables) {
			// Sizes 1, 2, 3, 4, and 5, two to a byte.
			st.stsz, st.stz2 = nil, stz2(4, 0x12, 0x34, 0x50)
		}, []Sample{{0, 1000, 1}, {300 * ms, 2003, 4}}},
		{"uniform stsz", func(st *sampleTables) {
			st.stsz = append(make([]byte, 4), append(u32(7), u32(5)...)...)
		}, []Sample{{0, 1000, 7}, {300 * ms, 2007, 7}}},
		{"ctts", func(st *sampleTables) {
			// Sample 1 is presented 0.2 seconds late, the rest 0.1.
			st.ctts = table(2, 1, 200, 4, 100)
		}, []Sample{{200 * ms, 1000, 10}, {400 * ms, 2030, 40}}},
		{"ctts v1", func(st *sampleTables) {
			st.ctts = table(2, 3, 50, 2, 0xffffff9c) // 50, then -100
			st.ctts[0] = 1
		}, []Sample{{50 * ms, 1000, 10}, {200 * ms, 2030, 40}}},
	} {
		st := testTables()
		tt.modify(st)
		got, err := st.syncSamples(1000, 0)
		if err != nil {
			t.Errorf("%s: %v", tt.name, err)
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: syncSamples = %+v; want %+v", tt.name, got, tt.want)
		}
	}
}

func TestSyncSamplesErrors(t *testing.T) {
	for _, tt := range []struct {
		name   string
		modify func(*sampleTables)
	}{
		{"no stsz", func(st *sampleTables) { st.stsz = nil }},
		{"no stco", func(st *sampleTables) { st.stco = nil }},
		{"no stsc", func(st *sampleTables) { st.stsc = nil }},
		{"no stts", func(st *sampleTables) { st.stts = nil }},
		{"sync sample 0", func(st *sampleTables) { st.stss = table(1, 0) }},
		{"sync sample past end", func(st *sampleTables) { st.stss = table(1, 6) }},
		{"sync samples out of order", func(st *sampleTables) { st.stss = table(2, 4, 1) }},
		{"stss too short", func(st *sampleTables) { st.stss = table(3, 1, 4) }},
		{"stsc chunk 0", func(st *sampleTables) { st.stsc = table(1, 0, 5, 1) }},
		{"stsc past chunks", func(st *sampleTables) { st.stsc = table(2, 1, 2, 1, 9, 3, 1) }},
		{"stsc too few samples", func(st *sampleTables) { st.stsc = table(1, 1, 1, 1) }},
		{"bad stz2 field", func(st *sampleTables) { st.stsz, st.stz2 = nil, []byte{0, 0, 0, 0, 0, 0, 0, 3, 0, 0, 0, 1, 0} }},
	} {
		st := testTables()
		tt.modify(st)
		if _, err := st.syncSamples(1000, 0); err == nil {
			t.Errorf("%s: syncSamples succeeded; want error", tt.name)
		}
	}
	if _, err := testTables().syncSamples(0, 0); err == nil {
		t.Error("syncSamples with no timescale succeeded; want error")
	}

	// Presentation starting at media time 0.2 seconds, per an edit
	// list, which moves the first sample to before the start.
	got, err := testTables().syncSamples(1000, 200)
	if want := []Sample{{0, 1000, 10}, {100 * time.Millisecond, 2030, 40}}; err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("syncSamples from 200 = %+v, %v; want %+v", got, err, want)
	}
}

func TestParseEditList(t *testing.T) {
	for _, tt := range []struct {
		name string
		body []byte
		want int64
	}{
		{"v0", table(1, 3000, 1024, 1<<16), 1024},
		{"empty edit first", table(2, 500, 0xffffffff, 1<<16, 3000, 2048, 1<<16), 2048},
		{"v1", append([]byte{1, 0, 0, 0}, append(u32(1),
			append(binary.BigEndian.AppendUint64(nil, 3000), append(binary.BigEndian.AppendUint64(nil, 1<<40), u32(1<<16)...)...)...)...), 1 << 40},
		{"short", table(2, 3000, 1024), 0},
	} {
		tr := new(track)
		tr.parseEditList(tt.body)
		if tr.mediaStart != tt.want {
			t.Errorf("%s: mediaStart = %d; want %d", tt.name, tr.mediaStart, tt.want)
		}
	}
}

func TestDisplaySize(t *testing.T) {
	clap := func(wn, wd, hn, hd uint32) []byte {
		return box("clap", u32(wn), u32(wd), u32(hn), u32(hd), u32(0), u32(1), u32(0), u32(1))
	}
	for _, tt := range []struct {
		name string
		vt   VideoTrack
		w, h int
	}{
		{"plain", VideoTrack{Width: 1920, Height: 1080}, 1920, 1080},
		{"rotated", VideoTrack{Width: 1920, Height: 1080, Rotation: 270}, 1080, 1920},
		{"clean aperture", VideoTrack{Width: 1920, Height: 1440, CleanAperture: clap(1744, 1, 1308, 1)}, 1744, 1308},
		{"rotated aperture", VideoTrack{Width: 1920, Height: 1440, Rotation: 90, CleanAperture: clap(3488, 2, 1308, 1)}, 1308, 1744},
		{"aperture too big", VideoTrack{Width: 640, Height: 480, CleanAperture: clap(700, 1, 480, 1)}, 640, 480},
		{"zero denominator", VideoTrack{Width: 640, Height: 480, CleanAperture: clap(600, 0, 400, 1)}, 640, 480},
	} {
		if w, h := tt.vt.DisplaySize(); w != tt.w || h != tt.h {
			t.Errorf("%s: DisplaySize = %dx%d; want %dx%d", tt.name, w, h, tt.w, tt.h)
		}
	}
}

func TestReadVideoTrack(t *testing.T) {
	// See ../../video/still/testdata/gen.sh.
	b, err := os.ReadFile("../../video/still/testdata/fade.mov")
	if err != nil {
		t.Fatal(err)
	}
	vt, err := ReadVideoTrack(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	if vt.Codec != "hvc1" || vt.Width != 64 || vt.Height != 48 || vt.Rotation != 90 || !bytes.Equal(vt.Config[4:8], []byte("hvcC")) {
		t.Errorf("ReadVideoTrack = %s %dx%d rotated %d, config %q", vt.Codec, vt.Width, vt.Height, vt.Rotation, vt.Config[4:8])
	}
	if vt.Duration != 3*time.Second {
		t.Errorf("Duration = %v; want 3s", vt.Duration)
	}
	var times []time.Duration
	for _, s := range vt.SyncSamples {
		times = append(times, s.Time)
		// HEVC samples are NAL units with 4 byte lengths.
		if n := int64(binary.BigEndian.Uint32(b[s.Offset:])); n <= 0 || n+4 > s.Size {
			t.Errorf("sample at %v doesn't start with a NAL unit length: %d of %d", s.Time, n, s.Size)
		}
	}
	ms := time.Millisecond
	if want := []time.Duration{0, 500 * ms, 1000 * ms, 1500 * ms, 2000 * ms, 2500 * ms}; !reflect.DeepEqual(times, want) {
		t.Errorf("sync sample times = %v; want %v", times, want)
	}

	if _, err := ReadVideoTrack(bytes.NewReader(liveMovieWithoutVideo()), int64(len(liveMovieWithoutVideo()))); err != ErrNoVideo {
		t.Errorf("ReadVideoTrack of a movie without video: %v; want ErrNoVideo", err)
	}
}

func TestReadVideoTrackColor(t *testing.T) {
	// See ../../video/still/testdata/gen.sh. ffmpeg writes QuickTime
	// ("nclc") color information in MOV files, like iPhones.
	for _, tt := range []struct {
		name string
		want ColorInfo
	}{
		{"hdr.mov", ColorInfo{Known: true, Primaries: PrimariesBT2020, Transfer: TransferHLG, Matrix: 9}},
		{"p3.mov", ColorInfo{Known: true, Primaries: PrimariesDisplayP3, Transfer: 1, Matrix: 6}},
	} {
		b, err := os.ReadFile("../../video/still/testdata/" + tt.name)
		if err != nil {
			t.Fatal(err)
		}
		vt, err := ReadVideoTrack(bytes.NewReader(b), int64(len(b)))
		if err != nil {
			t.Fatal(err)
		}
		if vt.ColorInfo != tt.want {
			t.Errorf("%s: ColorInfo = %+v; want %+v", tt.name, vt.ColorInfo, tt.want)
		}
	}
}

func liveMovieWithoutVideo() []byte {
	return movie(10, 600, 444, item{KeyContentIdentifier, 1, []byte("id")})
}
