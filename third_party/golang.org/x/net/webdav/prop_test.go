// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package webdav

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"testing"
)

func TestMemPS(t *testing.T) {
	// calcProps calculates the getlastmodified and getetag DAV: property
	// values in pstats for resource name in file-system fs.
	calcProps := func(name string, fs FileSystem, pstats []Propstat) error {
		fi, err := fs.Stat(name)
		if err != nil {
			return err
		}
		for _, pst := range pstats {
			for i, p := range pst.Props {
				switch p.XMLName {
				case xml.Name{Space: "DAV:", Local: "getlastmodified"}:
					p.InnerXML = []byte(fi.ModTime().Format(http.TimeFormat))
					pst.Props[i] = p
				case xml.Name{Space: "DAV:", Local: "getetag"}:
					if fi.IsDir() {
						continue
					}
					p.InnerXML = []byte(detectETag(fi))
					pst.Props[i] = p
				}
			}
		}
		return nil
	}

	type propOp struct {
		op            string
		name          string
		propnames     []xml.Name
		wantNames     []xml.Name
		wantPropstats []Propstat
	}

	testCases := []struct {
		desc    string
		buildfs []string
		propOp  []propOp
	}{{
		"propname",
		[]string{"mkdir /dir", "touch /file"},
		[]propOp{{
			op:   "propname",
			name: "/dir",
			wantNames: []xml.Name{
				xml.Name{Space: "DAV:", Local: "resourcetype"},
				xml.Name{Space: "DAV:", Local: "displayname"},
				xml.Name{Space: "DAV:", Local: "getcontentlength"},
				xml.Name{Space: "DAV:", Local: "getlastmodified"},
				xml.Name{Space: "DAV:", Local: "getcontenttype"},
			},
		}, {
			op:   "propname",
			name: "/file",
			wantNames: []xml.Name{
				xml.Name{Space: "DAV:", Local: "resourcetype"},
				xml.Name{Space: "DAV:", Local: "displayname"},
				xml.Name{Space: "DAV:", Local: "getcontentlength"},
				xml.Name{Space: "DAV:", Local: "getlastmodified"},
				xml.Name{Space: "DAV:", Local: "getcontenttype"},
				xml.Name{Space: "DAV:", Local: "getetag"},
			},
		}},
	}, {
		"allprop dir and file",
		[]string{"mkdir /dir", "write /file foobarbaz"},
		[]propOp{{
			op:   "allprop",
			name: "/dir",
			wantPropstats: []Propstat{{
				Status: http.StatusOK,
				Props: []Property{{
					XMLName:  xml.Name{Space: "DAV:", Local: "resourcetype"},
					InnerXML: []byte(`<collection xmlns="DAV:"/>`),
				}, {
					XMLName:  xml.Name{Space: "DAV:", Local: "displayname"},
					InnerXML: []byte("dir"),
				}, {
					XMLName:  xml.Name{Space: "DAV:", Local: "getcontentlength"},
					InnerXML: []byte("0"),
				}, {
					XMLName:  xml.Name{Space: "DAV:", Local: "getlastmodified"},
					InnerXML: nil, // Calculated during test.
				}, {
					XMLName:  xml.Name{Space: "DAV:", Local: "getcontenttype"},
					InnerXML: []byte("text/plain; charset=utf-8"),
				}},
			}},
		}, {
			op:   "allprop",
			name: "/file",
			wantPropstats: []Propstat{{
				Status: http.StatusOK,
				Props: []Property{{
					XMLName:  xml.Name{Space: "DAV:", Local: "resourcetype"},
					InnerXML: []byte(""),
				}, {
					XMLName:  xml.Name{Space: "DAV:", Local: "displayname"},
					InnerXML: []byte("file"),
				}, {
					XMLName:  xml.Name{Space: "DAV:", Local: "getcontentlength"},
					InnerXML: []byte("9"),
				}, {
					XMLName:  xml.Name{Space: "DAV:", Local: "getlastmodified"},
					InnerXML: nil, // Calculated during test.
				}, {
					XMLName:  xml.Name{Space: "DAV:", Local: "getcontenttype"},
					InnerXML: []byte("text/plain; charset=utf-8"),
				}, {
					XMLName:  xml.Name{Space: "DAV:", Local: "getetag"},
					InnerXML: nil, // Calculated during test.
				}},
			}},
		}, {
			op:   "allprop",
			name: "/file",
			propnames: []xml.Name{
				{"DAV:", "resourcetype"},
				{"foo", "bar"},
			},
			wantPropstats: []Propstat{{
				Status: http.StatusOK,
				Props: []Property{{
					XMLName:  xml.Name{Space: "DAV:", Local: "resourcetype"},
					InnerXML: []byte(""),
				}, {
					XMLName:  xml.Name{Space: "DAV:", Local: "displayname"},
					InnerXML: []byte("file"),
				}, {
					XMLName:  xml.Name{Space: "DAV:", Local: "getcontentlength"},
					InnerXML: []byte("9"),
				}, {
					XMLName:  xml.Name{Space: "DAV:", Local: "getlastmodified"},
					InnerXML: nil, // Calculated during test.
				}, {
					XMLName:  xml.Name{Space: "DAV:", Local: "getcontenttype"},
					InnerXML: []byte("text/plain; charset=utf-8"),
				}, {
					XMLName:  xml.Name{Space: "DAV:", Local: "getetag"},
					InnerXML: nil, // Calculated during test.
				}}}, {
				Status: http.StatusNotFound,
				Props: []Property{{
					XMLName: xml.Name{Space: "foo", Local: "bar"},
				}}},
			},
		}},
	}, {
		"propfind DAV:resourcetype",
		[]string{"mkdir /dir", "touch /file"},
		[]propOp{{
			op:        "propfind",
			name:      "/dir",
			propnames: []xml.Name{{"DAV:", "resourcetype"}},
			wantPropstats: []Propstat{{
				Status: http.StatusOK,
				Props: []Property{{
					XMLName:  xml.Name{Space: "DAV:", Local: "resourcetype"},
					InnerXML: []byte(`<collection xmlns="DAV:"/>`),
				}},
			}},
		}, {
			op:        "propfind",
			name:      "/file",
			propnames: []xml.Name{{"DAV:", "resourcetype"}},
			wantPropstats: []Propstat{{
				Status: http.StatusOK,
				Props: []Property{{
					XMLName:  xml.Name{Space: "DAV:", Local: "resourcetype"},
					InnerXML: []byte(""),
				}},
			}},
		}},
	}, {
		"propfind unsupported DAV properties",
		[]string{"mkdir /dir"},
		[]propOp{{
			op:        "propfind",
			name:      "/dir",
			propnames: []xml.Name{{"DAV:", "getcontentlanguage"}},
			wantPropstats: []Propstat{{
				Status: http.StatusNotFound,
				Props: []Property{{
					XMLName: xml.Name{Space: "DAV:", Local: "getcontentlanguage"},
				}},
			}},
		}, {
			op:        "propfind",
			name:      "/dir",
			propnames: []xml.Name{{"DAV:", "creationdate"}},
			wantPropstats: []Propstat{{
				Status: http.StatusNotFound,
				Props: []Property{{
					XMLName: xml.Name{Space: "DAV:", Local: "creationdate"},
				}},
			}},
		}},
	}, {
		"propfind getetag for files but not for directories",
		[]string{"mkdir /dir", "touch /file"},
		[]propOp{{
			op:        "propfind",
			name:      "/dir",
			propnames: []xml.Name{{"DAV:", "getetag"}},
			wantPropstats: []Propstat{{
				Status: http.StatusNotFound,
				Props: []Property{{
					XMLName: xml.Name{Space: "DAV:", Local: "getetag"},
				}},
			}},
		}, {
			op:        "propfind",
			name:      "/file",
			propnames: []xml.Name{{"DAV:", "getetag"}},
			wantPropstats: []Propstat{{
				Status: http.StatusOK,
				Props: []Property{{
					XMLName:  xml.Name{Space: "DAV:", Local: "getetag"},
					InnerXML: nil, // Calculated during test.
				}},
			}},
		}},
	}, {
		"bad: propfind unknown property",
		[]string{"mkdir /dir"},
		[]propOp{{
			op:        "propfind",
			name:      "/dir",
			propnames: []xml.Name{{"foo:", "bar"}},
			wantPropstats: []Propstat{{
				Status: http.StatusNotFound,
				Props: []Property{{
					XMLName: xml.Name{Space: "foo:", Local: "bar"},
				}},
			}},
		}},
	}}

	for _, tc := range testCases {
		fs, err := buildTestFS(tc.buildfs)
		if err != nil {
			t.Fatalf("%s: cannot create test filesystem: %v", tc.desc, err)
		}
		ls := NewMemLS()
		ps := NewMemPS(fs, ls)
		for _, op := range tc.propOp {
			desc := fmt.Sprintf("%s: %s %s", tc.desc, op.op, op.name)
			if err = calcProps(op.name, fs, op.wantPropstats); err != nil {
				t.Fatalf("%s: calcProps: %v", desc, err)
			}

			// Call property system.
			var propstats []Propstat
			switch op.op {
			case "propname":
				names, err := ps.Propnames(op.name)
				if err != nil {
					t.Errorf("%s: got error %v, want nil", desc, err)
					continue
				}
				sort.Sort(byXMLName(names))
				sort.Sort(byXMLName(op.wantNames))
				if !reflect.DeepEqual(names, op.wantNames) {
					t.Errorf("%s: names\ngot  %q\nwant %q", desc, names, op.wantNames)
				}
				continue
			case "allprop":
				propstats, err = ps.Allprop(op.name, op.propnames)
			case "propfind":
				propstats, err = ps.Find(op.name, op.propnames)
			default:
				t.Fatalf("%s: %s not implemented", desc, op.op)
			}
			if err != nil {
				t.Errorf("%s: got error %v, want nil", desc, err)
				continue
			}
			// Compare return values from allprop or propfind.
			for _, pst := range propstats {
				sort.Sort(byPropname(pst.Props))
			}
			for _, pst := range op.wantPropstats {
				sort.Sort(byPropname(pst.Props))
			}
			sort.Sort(byStatus(propstats))
			sort.Sort(byStatus(op.wantPropstats))
			if !reflect.DeepEqual(propstats, op.wantPropstats) {
				t.Errorf("%s: propstat\ngot  %q\nwant %q", desc, propstats, op.wantPropstats)
			}
		}
	}
}

func cmpXMLName(a, b xml.Name) bool {
	if a.Space != b.Space {
		return a.Space < b.Space
	}
	return a.Local < b.Local
}

type byXMLName []xml.Name

func (b byXMLName) Len() int {
	return len(b)
}
func (b byXMLName) Swap(i, j int) {
	b[i], b[j] = b[j], b[i]
}
func (b byXMLName) Less(i, j int) bool {
	return cmpXMLName(b[i], b[j])
}

type byPropname []Property

func (b byPropname) Len() int {
	return len(b)
}
func (b byPropname) Swap(i, j int) {
	b[i], b[j] = b[j], b[i]
}
func (b byPropname) Less(i, j int) bool {
	return cmpXMLName(b[i].XMLName, b[j].XMLName)
}

type byStatus []Propstat

func (b byStatus) Len() int {
	return len(b)
}
func (b byStatus) Swap(i, j int) {
	b[i], b[j] = b[j], b[i]
}
func (b byStatus) Less(i, j int) bool {
	return b[i].Status < b[j].Status
}
