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
		patches       []Proppatch
		wantNames     []xml.Name
		wantPropstats []Propstat
	}

	testCases := []struct {
		desc       string
		mutability Mutability
		buildfs    []string
		propOp     []propOp
	}{{
		desc:    "propname",
		buildfs: []string{"mkdir /dir", "touch /file"},
		propOp: []propOp{{
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
		desc:    "allprop dir and file",
		buildfs: []string{"mkdir /dir", "write /file foobarbaz"},
		propOp: []propOp{{
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
		desc:    "propfind DAV:resourcetype",
		buildfs: []string{"mkdir /dir", "touch /file"},
		propOp: []propOp{{
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
		desc:    "propfind unsupported DAV properties",
		buildfs: []string{"mkdir /dir"},
		propOp: []propOp{{
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
		desc:    "propfind getetag for files but not for directories",
		buildfs: []string{"mkdir /dir", "touch /file"},
		propOp: []propOp{{
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
		desc:       "proppatch property on read-only property system",
		buildfs:    []string{"mkdir /dir"},
		mutability: ReadOnly,
		propOp: []propOp{{
			op:   "proppatch",
			name: "/dir",
			patches: []Proppatch{{
				Props: []Property{{
					XMLName: xml.Name{Space: "foo", Local: "bar"},
				}},
			}},
			wantPropstats: []Propstat{{
				Status: http.StatusForbidden,
				Props: []Property{{
					XMLName: xml.Name{Space: "foo", Local: "bar"},
				}},
			}},
		}, {
			op:   "proppatch",
			name: "/dir",
			patches: []Proppatch{{
				Props: []Property{{
					XMLName: xml.Name{Space: "DAV:", Local: "getetag"},
				}},
			}},
			wantPropstats: []Propstat{{
				Status: http.StatusForbidden,
				Props: []Property{{
					XMLName: xml.Name{Space: "DAV:", Local: "getetag"},
				}},
			}},
		}},
	}, {
		desc:       "proppatch dead property",
		buildfs:    []string{"mkdir /dir"},
		mutability: ReadWrite,
		propOp: []propOp{{
			op:   "proppatch",
			name: "/dir",
			patches: []Proppatch{{
				Props: []Property{{
					XMLName:  xml.Name{Space: "foo", Local: "bar"},
					InnerXML: []byte("baz"),
				}},
			}},
			wantPropstats: []Propstat{{
				Status: http.StatusOK,
				Props: []Property{{
					XMLName: xml.Name{Space: "foo", Local: "bar"},
				}},
			}},
		}, {
			op:        "propfind",
			name:      "/dir",
			propnames: []xml.Name{{Space: "foo", Local: "bar"}},
			wantPropstats: []Propstat{{
				Status: http.StatusOK,
				Props: []Property{{
					XMLName:  xml.Name{Space: "foo", Local: "bar"},
					InnerXML: []byte("baz"),
				}},
			}},
		}},
	}, {
		desc:       "proppatch dead property with failed dependency",
		buildfs:    []string{"mkdir /dir"},
		mutability: ReadWrite,
		propOp: []propOp{{
			op:   "proppatch",
			name: "/dir",
			patches: []Proppatch{{
				Props: []Property{{
					XMLName:  xml.Name{Space: "foo", Local: "bar"},
					InnerXML: []byte("baz"),
				}},
			}, {
				Props: []Property{{
					XMLName:  xml.Name{Space: "DAV:", Local: "displayname"},
					InnerXML: []byte("xxx"),
				}},
			}},
			wantPropstats: []Propstat{{
				Status: http.StatusForbidden,
				Props: []Property{{
					XMLName: xml.Name{Space: "DAV:", Local: "displayname"},
				}},
			}, {
				Status: StatusFailedDependency,
				Props: []Property{{
					XMLName: xml.Name{Space: "foo", Local: "bar"},
				}},
			}},
		}, {
			op:        "propfind",
			name:      "/dir",
			propnames: []xml.Name{{Space: "foo", Local: "bar"}},
			wantPropstats: []Propstat{{
				Status: http.StatusNotFound,
				Props: []Property{{
					XMLName: xml.Name{Space: "foo", Local: "bar"},
				}},
			}},
		}},
	}, {
		desc:       "proppatch remove dead property",
		buildfs:    []string{"mkdir /dir"},
		mutability: ReadWrite,
		propOp: []propOp{{
			op:   "proppatch",
			name: "/dir",
			patches: []Proppatch{{
				Props: []Property{{
					XMLName:  xml.Name{Space: "foo", Local: "bar"},
					InnerXML: []byte("baz"),
				}, {
					XMLName:  xml.Name{Space: "spam", Local: "ham"},
					InnerXML: []byte("eggs"),
				}},
			}},
			wantPropstats: []Propstat{{
				Status: http.StatusOK,
				Props: []Property{{
					XMLName: xml.Name{Space: "foo", Local: "bar"},
				}, {
					XMLName: xml.Name{Space: "spam", Local: "ham"},
				}},
			}},
		}, {
			op:   "propfind",
			name: "/dir",
			propnames: []xml.Name{
				{Space: "foo", Local: "bar"},
				{Space: "spam", Local: "ham"},
			},
			wantPropstats: []Propstat{{
				Status: http.StatusOK,
				Props: []Property{{
					XMLName:  xml.Name{Space: "foo", Local: "bar"},
					InnerXML: []byte("baz"),
				}, {
					XMLName:  xml.Name{Space: "spam", Local: "ham"},
					InnerXML: []byte("eggs"),
				}},
			}},
		}, {
			op:   "proppatch",
			name: "/dir",
			patches: []Proppatch{{
				Remove: true,
				Props: []Property{{
					XMLName: xml.Name{Space: "foo", Local: "bar"},
				}},
			}},
			wantPropstats: []Propstat{{
				Status: http.StatusOK,
				Props: []Property{{
					XMLName: xml.Name{Space: "foo", Local: "bar"},
				}},
			}},
		}, {
			op:   "propfind",
			name: "/dir",
			propnames: []xml.Name{
				{Space: "foo", Local: "bar"},
				{Space: "spam", Local: "ham"},
			},
			wantPropstats: []Propstat{{
				Status: http.StatusNotFound,
				Props: []Property{{
					XMLName: xml.Name{Space: "foo", Local: "bar"},
				}},
			}, {
				Status: http.StatusOK,
				Props: []Property{{
					XMLName:  xml.Name{Space: "spam", Local: "ham"},
					InnerXML: []byte("eggs"),
				}},
			}},
		}},
	}, {
		desc:       "propname with dead property",
		buildfs:    []string{"touch /file"},
		mutability: ReadWrite,
		propOp: []propOp{{
			op:   "proppatch",
			name: "/file",
			patches: []Proppatch{{
				Props: []Property{{
					XMLName:  xml.Name{Space: "foo", Local: "bar"},
					InnerXML: []byte("baz"),
				}},
			}},
			wantPropstats: []Propstat{{
				Status: http.StatusOK,
				Props: []Property{{
					XMLName: xml.Name{Space: "foo", Local: "bar"},
				}},
			}},
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
				xml.Name{Space: "foo", Local: "bar"},
			},
		}},
	}, {
		desc:       "proppatch remove unknown dead property",
		buildfs:    []string{"mkdir /dir"},
		mutability: ReadWrite,
		propOp: []propOp{{
			op:   "proppatch",
			name: "/dir",
			patches: []Proppatch{{
				Remove: true,
				Props: []Property{{
					XMLName: xml.Name{Space: "foo", Local: "bar"},
				}},
			}},
			wantPropstats: []Propstat{{
				Status: http.StatusOK,
				Props: []Property{{
					XMLName: xml.Name{Space: "foo", Local: "bar"},
				}},
			}},
		}},
	}, {
		desc:    "bad: propfind unknown property",
		buildfs: []string{"mkdir /dir"},
		propOp: []propOp{{
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
		ps := NewMemPS(fs, ls, tc.mutability)
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
			case "proppatch":
				propstats, err = ps.Patch(op.name, op.patches)
			default:
				t.Fatalf("%s: %s not implemented", desc, op.op)
			}
			if err != nil {
				t.Errorf("%s: got error %v, want nil", desc, err)
				continue
			}
			// Compare return values from allprop, propfind or proppatch.
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
