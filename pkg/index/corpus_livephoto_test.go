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

package index

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/test"
)

func TestCorpusLivePhotoPartners(t *testing.T) {
	ref := blob.RefFromString
	rows := make(map[string]string)
	file := func(name, mime, contentID string) {
		file, whole := ref(name+" file"), ref(name+" whole")
		rows[keyFileInfo.Key(file)] = fmt.Sprintf("10|%s|%s|%s", urle(name), urle(mime), whole)
		rows[keyWholeToFileRef.Key(whole, file)] = "1"
		if contentID != "" {
			rows[keyMediaTag.Key(whole, MediaTagContentIdentifier)] = keyMediaTag.Val(contentID)
		}
	}
	file("still", "image/heic", "ID-A")
	file("video", "video/quicktime", "ID-A")
	file("other video", "video/quicktime", "ID-B")
	file("plain", "image/jpeg", "")
	// A second upload of the still image, with the same contents.
	rows[keyWholeToFileRef.Key(ref("still whole"), ref("still copy file"))] = "1"
	rows[keyFileInfo.Key(ref("still copy file"))] = fmt.Sprintf("10|still|%s|%s", urle("image/heic"), ref("still whole"))
	// QuickTime camera metadata.
	rows[keyMediaTag.Key(ref("video whole"), MediaTagMake)] = "Apple"
	rows[keyMediaTag.Key(ref("video whole"), MediaTagModel)] = keyMediaTag.Val("iPhone 16 Pro")

	for _, mode := range []string{"startup", "incremental"} {
		t.Run(mode, func(t *testing.T) {
			var c *Corpus
			if mode == "startup" {
				kv := sorted.NewMemoryKeyValue()
				t.Cleanup(func() { kv.Close() })
				for k, v := range rows {
					if err := kv.Set(k, v); err != nil {
						t.Fatal(err)
					}
				}
				var err error
				c, err = newCorpusFromStorage(kv, test.NewLogger(t, ""))
				if err != nil {
					t.Fatal(err)
				}
			} else {
				c = newLoggingCorpus(t)
				if err := c.addBlob(context.Background(), ref("received"), &mutationMap{kv: rows}); err != nil {
					t.Fatal(err)
				}
			}
			sortedRefs := func(names ...string) []blob.Ref {
				var refs []blob.Ref
				for _, n := range names {
					refs = append(refs, ref(n))
				}
				if len(refs) == 2 && refs[1].Less(refs[0]) {
					refs[0], refs[1] = refs[1], refs[0]
				}
				return refs
			}
			tests := []struct {
				file           string
				contentID      string
				stills, videos []blob.Ref
			}{
				{"still file", "ID-A", nil, sortedRefs("video file")},
				{"still copy file", "ID-A", nil, sortedRefs("video file")},
				{"video file", "ID-A", sortedRefs("still file", "still copy file"), nil},
				{"other video file", "ID-B", nil, nil},
				{"plain file", "", nil, nil},
				{"unknown file", "", nil, nil},
			}
			for _, tt := range tests {
				id, stills, videos := c.LivePhotoPartners(ref(tt.file))
				if id != tt.contentID || !reflect.DeepEqual(stills, tt.stills) || !reflect.DeepEqual(videos, tt.videos) {
					t.Errorf("LivePhotoPartners(%s) = %q, %v, %v; want %q, %v, %v",
						tt.file, id, stills, videos, tt.contentID, tt.stills, tt.videos)
				}
			}
			if make, model, ok := c.FileCamera(ref("video file")); make != "Apple" || model != "iPhone 16 Pro" || !ok {
				t.Errorf("video FileCamera = %q, %q, %v; want QuickTime make and model", make, model, ok)
			}
		})
	}
}
