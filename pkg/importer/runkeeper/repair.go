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
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	gpxRepairVersion     = "1"
	attrGPXRepairVersion = "runkeeperGPXRepairVersion"
	attrGPXRepairCount   = "runkeeperGPXRepairCount"
)

var (
	errNestedTrackSegment = errors.New("misplaced or nested track segment")
	errGPXRepairRejected  = errors.New("GPX segment repair not applicable")
)

type repairElement struct {
	name       xml.Name
	namespace  string
	namespaces map[string]string
}

type repairReader struct {
	r   io.Reader
	err error
}

func (r *repairReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if err != nil && err != io.EOF {
		r.err = err
	}
	return n, err
}

// repairTrack inserts a missing segment close before a new segment begins.
// RawToken is used only to identify original byte spans; we enforce our own
// matching stack, not a generally lax XML decoder. Everything except inserted
// closes is copied verbatim, including comments, whitespace, and extensions.
// The caller must also strictly validate the resulting route before using it.
func repairTrack(src io.Reader, dst io.Writer) (int, error) {
	var pending bytes.Buffer
	input := &repairReader{r: io.LimitReader(src, maxRenderTrack+1)}
	d := xml.NewDecoder(io.TeeReader(input, &pending))
	var stack []repairElement
	roots, repairs, written := 0, 0, int64(0)
	write := func(p []byte) error {
		if int64(len(p)) > maxRenderTrack-written {
			return fmt.Errorf("%w: exceeds thumbnail byte limit", errGPXRepairRejected)
		}
		n, err := dst.Write(p)
		written += int64(n)
		if err == nil && n != len(p) {
			err = io.ErrShortWrite
		}
		return err
	}
	for {
		start := d.InputOffset()
		tok, err := d.RawToken()
		if input.err != nil {
			return repairs, input.err
		}
		if err == io.EOF {
			if roots != 1 || len(stack) != 0 || repairs == 0 {
				return repairs, errGPXRepairRejected
			}
			return repairs, nil
		}
		if err != nil {
			return repairs, fmt.Errorf("%w: %v", errGPXRepairRejected, err)
		}
		end := d.InputOffset()
		if end > maxRenderTrack {
			return repairs, fmt.Errorf("%w: exceeds thumbnail byte limit", errGPXRepairRejected)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			declarations := rawNamespaceDeclarations(t.Attr)
			ns := rawElementNamespace(t.Name, declarations, stack)
			if len(stack) == 0 {
				roots++
				if roots != 1 || t.Name.Local != "gpx" || ns != gpxNamespace {
					return repairs, errGPXRepairRejected
				}
				version := ""
				for _, a := range t.Attr {
					if a.Name.Space == "" && a.Name.Local == "version" {
						version = a.Value
					}
				}
				if version != "1.1" {
					return repairs, errGPXRepairRejected
				}
			}
			if ns == gpxNamespace && t.Name.Local == "trkseg" && repairPath(stack, "gpx", "trk", "trkseg") {
				previous := stack[len(stack)-1]
				// Closing the previous segment must not change namespace
				// bindings inherited by the new segment or its children.
				if len(previous.namespaces) != 0 {
					return repairs, errGPXRepairRejected
				}
				name := previous.name.Local
				if previous.name.Space != "" {
					name = previous.name.Space + ":" + name
				}
				if err := write([]byte("</" + name + ">")); err != nil {
					return repairs, err
				}
				stack = stack[:len(stack)-1]
				repairs++
			}
			if len(stack) >= 64 {
				return repairs, fmt.Errorf("%w: XML nesting too deep", errGPXRepairRejected)
			}
			stack = append(stack, repairElement{name: t.Name, namespace: ns, namespaces: declarations})
		case xml.EndElement:
			if len(stack) == 0 || stack[len(stack)-1].name != t.Name {
				return repairs, errGPXRepairRejected
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) == 0 && strings.TrimSpace(string(t)) != "" {
				return repairs, errGPXRepairRejected
			}
		}
		span := int(end - start)
		if span > pending.Len() {
			return repairs, errors.New("runkeeper: missing captured XML bytes")
		}
		if err := write(pending.Next(span)); err != nil {
			return repairs, err
		}
	}
}

// Retain only namespace declarations, so resolving each point doesn't rescan
// arbitrary attributes on every ancestor. Maps are scoped, not copied per point.
func rawNamespaceDeclarations(attrs []xml.Attr) map[string]string {
	var declarations map[string]string
	for _, a := range attrs {
		prefix := ""
		switch {
		case a.Name.Space == "" && a.Name.Local == "xmlns":
		case a.Name.Space == "xmlns":
			prefix = a.Name.Local
		default:
			continue
		}
		if declarations == nil {
			declarations = make(map[string]string)
		}
		declarations[prefix] = a.Value
	}
	return declarations
}

// RawToken reports prefixes, not resolved URIs. Check the element's own
// declarations, then its ancestors, in at most the bounded XML depth.
func rawElementNamespace(name xml.Name, declarations map[string]string, stack []repairElement) string {
	if ns, ok := declarations[name.Space]; ok {
		return ns
	}
	for i := len(stack) - 1; i >= 0; i-- {
		if ns, ok := stack[i].namespaces[name.Space]; ok {
			return ns
		}
	}
	return ""
}

func repairPath(stack []repairElement, path ...string) bool {
	if len(stack) != len(path) {
		return false
	}
	for i, name := range path {
		if stack[i].namespace != gpxNamespace || stack[i].name.Local != name {
			return false
		}
	}
	return true
}

// repairedTrackFile returns a rewound, private temporary file only after a
// nonempty repair passes the same strict validation as an original GPX. Callers
// must close and remove the file. Rejected candidates are cleaned up here.
func repairedTrackFile(src memberSource, name, hash string) (_ *os.File, info trackInfo, retErr error) {
	input, err := src.OpenLimit(name, maxRenderTrack)
	if err != nil {
		return nil, info, err
	}
	defer input.Close()
	f, err := os.CreateTemp("", "perkeep-runkeeper-gpx-*")
	if err != nil {
		return nil, info, err
	}
	defer func() {
		if retErr != nil {
			removeTrackFile(f)
		}
	}()
	count, err := repairTrack(input, f)
	if err != nil {
		return nil, info, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, info, err
	}
	h := sha256.New()
	info, err = parseTrack(io.TeeReader(f, h), hash)
	if err != nil {
		var pathErr *os.PathError
		if errors.As(err, &pathErr) {
			return nil, info, err
		}
		return nil, info, fmt.Errorf("%w: strict validation: %v", errGPXRepairRejected, err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, info, err
	}
	info.RepairVersion = gpxRepairVersion
	info.RepairCount = count
	info.RepairedHash = hex.EncodeToString(h.Sum(nil))
	info.Warning = boundedWarning(fmt.Sprintf("repaired GPX %s: inserted %d missing segment closes (repair version %s)", name, count, gpxRepairVersion))
	return f, info, nil
}

func removeTrackFile(f *os.File) {
	f.Close()
	os.Remove(f.Name())
}
