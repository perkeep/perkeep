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
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver/memory"
	"perkeep.org/pkg/importer"
	"perkeep.org/pkg/jsonsign"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/schema/nodeattr"
	"perkeep.org/pkg/search"
)

type coreStorage struct {
	memory.Storage
	mu        sync.Mutex
	attrs     map[blob.Ref]url.Values
	failClaim string // fail the next set-attribute claim for this key
	failFile  string // "content" or the filename of the next file schema to reject
}

var errCoreClaimFailure = errors.New("injected claim storage failure")
var errCoreFileFailure = errors.New("injected file storage failure")

func (s *coreStorage) ReceiveBlob(ctx context.Context, ref blob.Ref, src io.Reader) (blob.SizedRef, error) {
	data, err := io.ReadAll(src)
	if err != nil {
		return blob.SizedRef{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, alreadyStored := s.Storage.BlobContents(ref)
	if alreadyStored {
		return s.Storage.ReceiveBlob(ctx, ref, bytes.NewReader(data))
	}
	b, parseErr := schema.BlobFromReader(ref, bytes.NewReader(data))
	if s.failFile != "" && ((s.failFile == "content" && (parseErr != nil || b.Type() == "")) ||
		(parseErr == nil && b.Type() == schema.TypeFile && b.FileName() == s.failFile)) {
		s.failFile = ""
		return blob.SizedRef{}, errCoreFileFailure
	}
	var operations []schema.AttributeClaim
	if parseErr == nil && b.Type() == schema.TypeClaim {
		claim, ok := b.AsClaim()
		if !ok {
			return blob.SizedRef{}, errors.New("invalid signed claim")
		}
		operations, err = claim.AttributeClaims()
		if err != nil {
			return blob.SizedRef{}, err
		}
		// Reject the entire envelope before storing or applying any child.
		for _, operation := range operations {
			if s.attrs[operation.Permanode] == nil {
				return blob.SizedRef{}, fmt.Errorf("claim for unknown permanode %v", operation.Permanode)
			}
		}
		for _, operation := range operations {
			if operation.Type == schema.SetAttributeClaim && operation.Attribute == s.failClaim {
				s.failClaim = ""
				return blob.SizedRef{}, errCoreClaimFailure
			}
		}
	}
	sb, err := s.Storage.ReceiveBlob(ctx, ref, bytes.NewReader(data))
	if err != nil {
		return sb, err
	}
	if parseErr != nil {
		return sb, nil // raw file content is not necessarily JSON
	}
	if s.attrs == nil {
		s.attrs = make(map[blob.Ref]url.Values)
	}
	if b.Type() == schema.TypePermanode {
		s.attrs[ref] = make(url.Values)
	}
	for _, operation := range operations {
		attrs := s.attrs[operation.Permanode]
		switch operation.Type {
		case schema.SetAttributeClaim:
			attrs.Set(operation.Attribute, operation.Value)
		case schema.AddAttributeClaim:
			if !slices.Contains(attrs[operation.Attribute], operation.Value) {
				attrs.Add(operation.Attribute, operation.Value)
			}
		case schema.DelAttributeClaim:
			if operation.Value == "" {
				attrs.Del(operation.Attribute)
			} else {
				values := attrs[operation.Attribute]
				kept := make([]string, 0, len(values))
				for _, value := range values {
					if value != operation.Value {
						kept = append(kept, value)
					}
				}
				attrs[operation.Attribute] = kept
			}
		}
	}
	return sb, nil
}

func (s *coreStorage) failNextClaim(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failClaim = key
}

func (s *coreStorage) failNextFileUpload(stage string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failFile = stage
}

func (s *coreStorage) Describe(_ context.Context, req *search.DescribeRequest) (*search.DescribeResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	attrs, ok := s.attrs[req.BlobRef]
	if !ok {
		return nil, fmt.Errorf("unknown permanode %v", req.BlobRef)
	}
	copyAttrs := make(url.Values, len(attrs))
	for key, values := range attrs {
		copyAttrs[key] = append([]string(nil), values...)
	}
	return &search.DescribeResponse{Meta: search.MetaMap{
		req.BlobRef.String(): {
			BlobRef: req.BlobRef,
			Permanode: &search.DescribedPermanode{
				Attr: copyAttrs,
			},
		},
	}}, nil
}

func (s *coreStorage) Query(_ context.Context, req *search.SearchQuery) (*search.SearchResult, error) {
	nodeType := "importer"
	if req.Expression == "attr:camliNodeType:importerAccount attr:importerType:runkeeper" {
		nodeType = "importerAccount"
	} else if req.Expression != "attr:camliNodeType:importer attr:importerType:runkeeper" {
		return nil, errors.New("unexpected search query in importer core test")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	res := &search.SearchResult{}
	for ref, attrs := range s.attrs {
		if attrs.Get("camliNodeType") == nodeType && attrs.Get("importerType") == "runkeeper" {
			res.Blobs = append(res.Blobs, &search.SearchResultBlob{Blob: ref})
		}
	}
	return res, nil
}

type fixture struct {
	rc *importer.RunContext
	s  *coreStorage
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	entity, err := jsonsign.EntityFromSecring("26F5ABDA", "../../jsonsign/testdata/test-secring.gpg")
	if err != nil {
		t.Fatal(err)
	}
	pub, err := jsonsign.ArmoredPublicKey(entity)
	if err != nil {
		t.Fatal(err)
	}
	ref := blob.RefFromString(pub)
	signer, err := schema.NewSigner(ref, strings.NewReader(pub), entity)
	if err != nil {
		t.Fatal(err)
	}
	s := new(coreStorage)
	if _, err := s.ReceiveBlob(context.Background(), ref, strings.NewReader(pub)); err != nil {
		t.Fatal(err)
	}
	h, err := importer.NewHost(importer.HostConfig{BaseURL: "https://perkeep.example", Prefix: "/importer/", Target: s, BlobSource: s, Signer: signer, Search: s, HTTPClient: &http.Client{Transport: denyTransport{}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := h.Close(); err != nil {
			t.Error(err)
		}
	})
	rc, err := importer.CreateAccount(h, "runkeeper")
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{rc, s}
}

type denyTransport struct{}

func (denyTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("network prohibited")
}

type zipSource struct {
	z     *zip.Reader
	names map[string]bool
}

func (z *zipSource) Open(name string) (io.ReadCloser, error) { return z.OpenLimit(name, 4<<30) }
func (z *zipSource) OpenLimit(name string, limit int64) (io.ReadCloser, error) {
	for _, f := range z.z.File {
		if f.Name == name {
			if f.UncompressedSize64 > uint64(limit) {
				return nil, errors.New("member too large")
			}
			return f.Open()
		}
	}
	return nil, os.ErrNotExist
}
func source(t *testing.T, files map[string]string) *zipSource {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	names := make([]string, 0, len(files))
	for k := range files {
		names = append(names, k)
	}
	slices.Sort(names)
	for _, name := range names {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.Write([]byte(files[name])); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(b.Bytes()), int64(b.Len()))
	if err != nil {
		t.Fatal(err)
	}
	src := &zipSource{z: z, names: make(map[string]bool)}
	for _, f := range z.File {
		src.names[f.Name] = true
	}
	return src
}

const idA = "11111111-1111-4111-8111-111111111111"
const idB = "22222222-2222-4222-8222-222222222222"
const idC = "33333333-3333-4333-8333-333333333333"
const goodGPX = `<?xml version="1.0"?><gpx xmlns="http://www.topografix.com/GPX/1/1" version="1.1"><trk><trkseg><trkpt lat="0" lon="0"><time>2024-01-02T03:04:05-08:00</time></trkpt><trkpt lat="1" lon="2"/></trkseg></trk></gpx>`

func synthetic() map[string]string {
	return map[string]string{
		"profile.json": `{"username":"optional-user","email":"Optional[owner@example.invalid]","name":"Test Person","distanceUnits":"mi"}`,
		"cardioActivities.csv": "Activity Id,Date,Type,Route Name,Distance (mi),Duration,Calories Burned,Climb (ft),Average Heart Rate (bpm),Friend's Tagged,Notes,GPX File,Unknown\n" +
			idA + ",2024-01-02 03:04:05,Running,Test run,1,25:01:02,2,10,100,Friend,Notes,shared.gpx,keep me\n" +
			idB + ",2024-01-02 03:04:05,Walking,,2,2:03,0,0,,,,shared.gpx,also keep\n",
		"shared.gpx":       goodGPX,
		"photos.csv":       "Activity Id, Image File Name, Upload date, Text, Location\n" + idA + ",image.jpg,unparsed date,photo note,unknown location\n",
		"image.jpg":        "synthetic image bytes",
		"steps.csv":        "Activity Id, Timestamp, Steps, Unknown sensor\n" + idB + ",0,3,b\n" + idA + ",0,5,a\n" + idB + ",1,4,c\n",
		"heartRate.csv":    "Activity Id, Timestamp, Heart Rate\n",
		"measurements.csv": "Date,Type,Value\n2024-01-01 00:00:00,weight,70\n",
	}
}
func (f *fixture) run(t *testing.T, src *zipSource) importProgress {
	t.Helper()
	var p importProgress
	if err := importArchive(f.rc, src, src.names, func(v importProgress) error { p = v; return nil }); err != nil {
		t.Fatal(err)
	}
	return p
}
func (f *fixture) activity(t *testing.T, id string) *importer.Object {
	t.Helper()
	parent, err := f.rc.RootNode().ChildPathObject("activities")
	if err != nil {
		t.Fatal(err)
	}
	obj, err := parent.ChildPathObject(id)
	if err != nil {
		t.Fatal(err)
	}
	return obj
}
func (f *fixture) file(t *testing.T, ref string) string {
	t.Helper()
	br, ok := blob.Parse(ref)
	if !ok {
		t.Fatalf("invalid file ref %q", ref)
	}
	r, err := schema.NewFileReader(context.Background(), f.s, br)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
func TestArchiveIncremental(t *testing.T) {
	f := newFixture(t)
	files := synthetic()
	src := source(t, files)
	p := f.run(t, src)
	if p.Done != 2 || p.Unchanged != 0 || p.WarningCount != 0 {
		t.Fatalf("progress: %+v", p)
	}
	a, b := f.activity(t, idA), f.activity(t, idB)
	if a.PermanodeRef() == b.PermanodeRef() {
		t.Fatal("shared GPX collapsed UUIDs")
	}
	for k, want := range map[string]string{nodeattr.Type: nodeattr.FitnessActivity, nodeattr.FitnessDistance: "1609.344", nodeattr.FitnessRecordedDuration: "90062", nodeattr.FitnessEnergy: "8368", "startDate": "2024-01-02T11:04:05Z", "latitude": "0", "longitude": "0", "runkeeperFriendsTagged": "Friend", nodeattr.Description: "Notes", attrVersion: importVersion} {
		if got := a.Attr(k); got != want {
			t.Errorf("%s=%q want %q", k, got, want)
		}
	}
	if a.Attr(nodeattr.FitnessMovingDuration) != "" {
		t.Fatal("fabricated moving duration")
	}
	if a.Attr(nodeattr.FitnessTrack) != b.Attr(nodeattr.FitnessTrack) {
		t.Fatal("shared track not reused")
	}
	if got := f.file(t, a.Attr(attrSourceGPX)); got != goodGPX {
		t.Fatal("source GPX altered")
	}
	if !strings.Contains(f.file(t, a.Attr(attrActivityJSON)), `"Unknown":"keep me"`) {
		t.Fatal("lost unknown source column")
	}
	if got := f.file(t, a.Attr(attrSteps)); !strings.Contains(got, `"Unknown sensor":"a"`) || strings.Contains(got, `"Steps":"3"`) {
		t.Fatalf("bad grouped sensors: %s", got)
	}
	var photos []photoInput
	if err := json.Unmarshal([]byte(f.file(t, a.Attr(attrPhotosJSON))), &photos); err != nil {
		t.Fatal(err)
	}
	if len(photos) != 1 || photos[0].Raw["Text"] != "photo note" || f.file(t, photos[0].Ref) != "synthetic image bytes" {
		t.Fatalf("photos not preserved: %+v", photos)
	}
	for _, k := range []string{"runkeeperProfileJSON", "runkeeperCardioCSV", "runkeeperPhotosCSV", "runkeeperMeasurementsCSV", "runkeeperStepsCSV", "runkeeperHeartRateCSV"} {
		if f.rc.AccountNode().Attr(k) == "" {
			t.Errorf("missing account source %s", k)
		}
	}
	before := f.s.NumBlobs()
	p = f.run(t, src)
	if p.Unchanged != 2 || f.s.NumBlobs() != before {
		t.Fatalf("repeat mutated blobs %d -> %d; %+v", before, f.s.NumBlobs(), p)
	}
	oldB := b.Attr(attrHash)
	oldA := a.PermanodeRef()
	files["steps.csv"] += idA + ",2,6,changed\n"
	p = f.run(t, source(t, files))
	if p.Unchanged != 1 || f.activity(t, idA).PermanodeRef() != oldA || f.activity(t, idB).Attr(attrHash) != oldB {
		t.Fatal("sensor change did not isolate update")
	}
	files["image.jpg"] = "changed image"
	p = f.run(t, source(t, files))
	if p.Unchanged != 1 {
		t.Fatal("photo content change ignored or changed unrelated activity")
	}
	files["shared.gpx"] = strings.Replace(goodGPX, `lat="1"`, `lat="3"`, 1)
	p = f.run(t, source(t, files))
	if p.Unchanged != 0 {
		t.Fatal("shared GPX change must update both activities")
	}
	files["cardioActivities.csv"] += idC + ",2024-01-03 00:00:00,Wheelchair,,0,0:00,,,,,,,new\n"
	p = f.run(t, source(t, files))
	if p.Done != 3 || p.Unchanged != 2 {
		t.Fatalf("added activity progress %+v", p)
	}
	c := f.activity(t, idC)
	if c.Attr("startDate") != "" || c.Attr(nodeattr.FitnessTrack) != "" || c.Attr(nodeattr.FitnessActivityType) != "other" || c.Attr("runkeeperActivityType") != "Wheelchair" {
		t.Fatal("missing timezone or classification mishandled")
	}
}

func TestFailedRefreshAtomicRetry(t *testing.T) {
	for _, stage := range []string{"claim", "activity.json", "shared.gpx", "photos.json", "steps.jsonl"} {
		t.Run(stage, func(t *testing.T) {
			f := newFixture(t)
			files := synthetic()
			f.run(t, source(t, files))
			a := f.activity(t, idA)
			old := make(map[string]string)
			for _, k := range append(slices.Clone(activityKeys), attrHash, attrVersion) {
				old[k] = a.Attr(k)
			}
			files["cardioActivities.csv"] = strings.Replace(files["cardioActivities.csv"], ",Friend,Notes,", ",Friend,edited notes,", 1)
			// Changed member schemas ensure upload-failure injection targets new content.
			files["shared.gpx"] = strings.Replace(goodGPX, `lat="1"`, `lat="4"`, 1)
			files["image.jpg"] = "new image"
			files["steps.csv"] += idA + ",3,8,new\n"
			if stage == "claim" {
				f.s.failNextClaim(attrHash)
			} else {
				f.s.failNextFileUpload(stage)
			}
			src := source(t, files)
			err := importArchive(f.rc, src, src.names, nil)
			if err == nil {
				t.Fatal("expected injected failure")
			}
			a = f.activity(t, idA)
			for k, v := range old {
				if a.Attr(k) != v {
					t.Errorf("failed refresh modified %s", k)
				}
			}
			f.run(t, src)
			a = f.activity(t, idA)
			if a.Attr(nodeattr.Description) != "edited notes" || a.Attr(attrHash) == old[attrHash] {
				t.Fatal("retry did not refresh stable activity")
			}
		})
	}
}
func TestMalformedTrackRemovesOnlyModeledTrack(t *testing.T) {
	f := newFixture(t)
	files := synthetic()
	f.run(t, source(t, files))
	a := f.activity(t, idA)
	ref := a.PermanodeRef()
	files["shared.gpx"] = strings.Replace(goodGPX, "</trkseg>", "", 1)
	p := f.run(t, source(t, files))
	a = f.activity(t, idA)
	if a.PermanodeRef() != ref || a.Attr(nodeattr.FitnessTrack) != "" || a.Attr("startDate") != "" || a.Attr("latitude") != "" || a.Attr(attrVersion) != importVersion || a.Attr(attrSourceGPX) == "" || p.WarningCount < 2 {
		t.Fatalf("bad malformed-track refresh: %+v", p)
	}
	if f.file(t, a.Attr(attrSourceGPX)) != files["shared.gpx"] {
		t.Fatal("malformed source lost")
	}
	before := f.s.NumBlobs()
	p = f.run(t, source(t, files))
	if p.Unchanged != 2 || f.s.NumBlobs() != before {
		t.Fatal("malformed source repeat churned")
	}
	// Edits after the XML error still invalidate the fingerprint.
	files["shared.gpx"] += "<!-- changed tail -->"
	p = f.run(t, source(t, files))
	if p.Unchanged != 0 {
		t.Fatal("malformed trailing source change ignored")
	}
}

// TestPersonalArchive is opt-in and never records personal data in fixtures.
// RUNKEEPER_TEST_ARCHIVE=/absolute/path/to/export.zip go test -run TestPersonalArchive -v
func TestPersonalArchive(t *testing.T) {
	name := os.Getenv("PERKEEP_RUNKEEPER_TEST_ARCHIVE")
	if name == "" {
		name = os.Getenv("RUNKEEPER_TEST_ARCHIVE")
	}
	if name == "" {
		t.Skip("set PERKEEP_RUNKEEPER_TEST_ARCHIVE for private local validation")
	}
	z, err := zip.OpenReader(name)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	src := &zipSource{z: &z.Reader, names: make(map[string]bool)}
	for _, m := range z.File {
		src.names[m.Name] = true
	}
	p, err := readProfile(src)
	if err != nil || p.UserName == "" || p.Email == "" {
		t.Fatal("profile identity missing", err)
	}
	f := newFixture(t)
	progress := f.run(t, src)
	if progress.Total != 855 || progress.Done != 855 {
		t.Fatalf("expected 855 source activities; %+v", progress)
	}
	counts := map[string]int{}
	for _, attrs := range f.s.attrs {
		if attrs.Get(nodeattr.Type) != nodeattr.FitnessActivity {
			continue
		}
		counts["activities"]++
		if attrs.Get(nodeattr.FitnessTrack) != "" {
			counts["tracks"]++
		}
		if attrs.Get(attrSourceGPX) != "" {
			counts["sourceTracks"]++
		}
		if attrs.Get(attrPhotosJSON) != "" {
			var photos []photoInput
			if err := json.Unmarshal([]byte(f.file(t, attrs.Get(attrPhotosJSON))), &photos); err != nil {
				t.Fatal(err)
			}
			counts["photos"] += len(photos)
		}
		if attrs.Get("startDate") == "" {
			counts["unzoned"]++
		}
		if attrs.Get(attrSteps) != "" {
			counts["sensorAttachments"]++
		}
	}
	want := map[string]int{"activities": 855, "tracks": 844, "sourceTracks": 850, "photos": 5, "unzoned": 11}
	for k, v := range want {
		if counts[k] != v {
			t.Errorf("%s=%d, want %d", k, counts[k], v)
		}
	}
	if counts["sensorAttachments"] == 0 {
		t.Fatal("missing sensor attachments")
	}
	before := f.s.NumBlobs()
	second := f.run(t, src)
	if second.Unchanged != 855 || f.s.NumBlobs() != before {
		t.Fatalf("personal archive repeat mutated blobs %d -> %d; %+v", before, f.s.NumBlobs(), second)
	}
	t.Logf("validated counts: %v; warnings=%d; repeat added zero blobs", counts, progress.WarningCount)
}

func TestIncompleteActivityRetry(t *testing.T) {
	f := newFixture(t)
	src := source(t, synthetic())
	f.s.failNextClaim(attrHash)
	if err := importArchive(f.rc, src, src.names, nil); err == nil {
		t.Fatal("expected first activity publication to fail")
	}
	a := f.activity(t, idA)
	ref := a.PermanodeRef()
	if a.Attr(nodeattr.Type) != "" || a.Attr(attrVersion) != "" || a.Attr(attrActivityJSON) != "" {
		t.Fatal("incomplete activity advertised data/completion")
	}
	p := f.run(t, src)
	a = f.activity(t, idA)
	if a.PermanodeRef() != ref || a.Attr(attrVersion) != importVersion || p.Unchanged != 0 {
		t.Fatal("retry did not finish the original activity permanode")
	}
}
func TestOptionalDataRemoval(t *testing.T) {
	f := newFixture(t)
	files := synthetic()
	f.run(t, source(t, files))
	files["cardioActivities.csv"] = strings.Replace(files["cardioActivities.csv"], ",100,Friend,Notes,shared.gpx,", ",100,,,,", 1)
	files["photos.csv"] = "Activity Id,Image File Name,Upload date,Text,Location\n"
	files["steps.csv"] = "Activity Id,Timestamp,Steps\n"
	p := f.run(t, source(t, files))
	a := f.activity(t, idA)
	for _, k := range []string{nodeattr.Description, "runkeeperFriendsTagged", nodeattr.FitnessTrack, "startDate", "latitude", "longitude", attrSourceGPX, attrPhotosJSON, attrSteps} {
		if a.Attr(k) != "" {
			t.Errorf("stale optional %s remains", k)
		}
	}
	if a.Attr(nodeattr.FitnessRecordedDuration) != "90062" || a.Attr(nodeattr.FitnessElapsedDuration) != "" {
		t.Fatal("provider timer mislabeled")
	}
	if p.Unchanged != 0 {
		t.Fatal("sensor removal must refresh both activities")
	}
	before := f.s.NumBlobs()
	f.run(t, source(t, files))
	if f.s.NumBlobs() != before {
		t.Fatal("optional removal repeat churned")
	}
}
