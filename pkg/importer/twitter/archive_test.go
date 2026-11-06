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

package twitter

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"
	"testing"

	"perkeep.org/pkg/importer"
	imptest "perkeep.org/pkg/importer/test"
	"perkeep.org/pkg/schema/nodeattr"
	"perkeep.org/pkg/types/camtypes"
)

const (
	testPhotoTweet = "1947347723776364797"
	testVideoTweet = "1947347723776364798"
	testRetweet    = "1947347723776364799"
)

// testArchive returns a ZIP of an archive of the modern kind, with a
// tweet of a photo, a tweet of a video, a geotagged retweet, two likes
// (one of a tweet from before IDs had times), the profile, a follow and
// two followers.
func testArchive(t *testing.T) *zip.Reader {
	js := func(global, json string) string { return "window.YTD." + global + ".part0 = " + json }
	members := map[string]string{
		"data/manifest.js": `window.__THAR_CONFIG = {"dataTypes": {
			"tweets": {"files": [{"fileName": "data/tweets.js", "count": "3"}]},
			"like": {"files": [{"fileName": "data/like.js", "count": "2"}]},
			"following": {"files": [{"fileName": "data/following.js", "count": "1"}]}}}`,
		"data/account.js": js("account", `[{"account": {"accountId": "650013", "username": "bradfitz", "accountDisplayName": "Brad Fitzpatrick", "email": "brad@example.test"}}]`),
		"data/tweets.js": js("tweets", fmt.Sprintf(`[
			{"tweet": {"id_str": %q, "full_text": "a photo &amp; text", "created_at": "Mon Jul 21 17:27:31 +0000 2025",
				"extended_entities": {"media": [{"type": "photo", "media_url_https": "https://pbs.twimg.com/media/Photo1.jpg"}]}}},
			{"tweet": {"id_str": %q, "full_text": "a video", "created_at": "Mon Jul 21 17:27:32 +0000 2025",
				"extended_entities": {"media": [{"type": "video", "media_url_https": "https://pbs.twimg.com/ext_tw_video_thumb/1/pu/img/Thumb.jpg",
					"video_info": {"variants": [
						{"bitrate": "256000", "content_type": "video/mp4", "url": "https://video.twimg.com/ext_tw_video/1/pu/vid/small.mp4?tag=12"},
						{"bitrate": "2176000", "content_type": "video/mp4", "url": "https://video.twimg.com/ext_tw_video/1/pu/vid/big.mp4?tag=12"},
						{"content_type": "application/x-mpegURL", "url": "https://video.twimg.com/ext_tw_video/1/pu/pl/x.m3u8"}]}}]}}},
			{"tweet": {"id_str": %q, "full_text": "RT @someone: hi", "created_at": "Mon Jul 21 17:27:33 +0000 2025",
				"coordinates": {"type": "Point", "coordinates": ["-122.4", "37.7"]}}}
		]`, testPhotoTweet, testVideoTweet, testRetweet)),
		"data/tweets_media/" + testPhotoTweet + "-Photo1.jpg": "photo bytes",
		"data/tweets_media/" + testVideoTweet + "-big.mp4":    "video bytes",
		"data/like.js": js("like", `[
			{"like": {"tweetId": "1584364413070761985", "fullText": "liked &amp; such", "expandedUrl": "https://twitter.com/i/web/status/1584364413070761985"}},
			{"like": {"tweetId": "1234567", "fullText": "an old one", "expandedUrl": "https://twitter.com/i/web/status/1234567"}}
		]`),
		"data/profile.js": js("profile", `[{"profile": {"description": {"bio": "my bio", "website": "https://bradfitz.com/", "location": "Seattle"},
			"avatarMediaUrl": "https://pbs.twimg.com/profile_images/1/Avatar.jpg", "headerMediaUrl": "https://pbs.twimg.com/profile_banners/650013/1452574191"}}]`),
		"data/profile_media/650013-Avatar.jpg":     "avatar bytes",
		"data/profile_media/650013-1452574191.jpg": "header bytes",
		"data/following.js":                        js("following", `[{"following": {"accountId": "12", "userLink": "https://twitter.com/intent/user?user_id=12"}}]`),
		"data/follower.js":                         js("follower", `[{"follower": {"accountId": "13"}}, {"follower": {"accountId": "14"}}]`),
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range members {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	return zr
}

func TestArchive(t *testing.T) {
	zr := testArchive(t)
	src, names := zipSource{zr}, zipNames(zr)
	id, err := inspectArchive(src, names)
	if err != nil {
		t.Fatal(err)
	}
	if want := (camtypes.ArchiveIdentity{UserID: "650013", UserName: "bradfitz", Name: "Brad Fitzpatrick", Email: "brad@example.test"}); id != want {
		t.Errorf("identity = %+v; want %+v", id, want)
	}

	h := imptest.NewMemoryHost(t, importer.HostConfig{})
	t.Cleanup(func() { h.Close() })
	rc, err := importer.CreateAccount(h, "twitter")
	if err != nil {
		t.Fatal(err)
	}
	acct := rc.AccountNode()
	if err := acct.SetAttrs(importer.AcctAttrUserID, "650013", importer.AcctAttrUserName, "bradfitz", importer.AcctAttrName, "Brad Fitzpatrick"); err != nil {
		t.Fatal(err)
	}
	child := func(path ...string) *importer.Object {
		t.Helper()
		n := rc.RootNode()
		for _, p := range path {
			var err error
			if n, err = n.ChildPathObject(p); err != nil {
				t.Fatal(err)
			}
		}
		return n
	}
	// The video tweet, imported through the API, with its preview image.
	apiTweet := child("tweets", testVideoTweet)
	if err := apiTweet.SetAttrs(attrImportMethod, "api", nodeattr.Content, "a video, from the API", "camliPath:Thumb.jpg", "sha224-0", nodeattr.CamliContentImage, "sha224-0"); err != nil {
		t.Fatal(err)
	}

	im := &imp{}
	var last camtypes.ArchiveImportProgress
	report := func(p camtypes.ArchiveImportProgress) error { last = p; return nil }
	if err := im.importArchive(rc, src, names, report); err != nil {
		t.Fatal(err)
	}

	photo := child("tweets", testPhotoTweet)
	if got := photo.Attr(nodeattr.Content); got != "a photo & text" {
		t.Errorf("photo tweet's text = %q", got)
	}
	if ref := photo.Attr("camliPath:Photo1.jpg"); ref == "" || photo.Attr(nodeattr.CamliContentImage) != ref {
		t.Errorf("photo tweet's photo = %q, image %q", ref, photo.Attr(nodeattr.CamliContentImage))
	}
	video := child("tweets", testVideoTweet)
	if got := video.Attr(nodeattr.Content); got != "a video, from the API" {
		t.Errorf("API tweet's text = %q; want the API's", got)
	}
	if video.Attr("camliPath:big.mp4") == "" || video.Attr(nodeattr.CamliContentImage) != "sha224-0" {
		t.Errorf("API tweet's video = %q, image %q; want the archive's video, and the API's image", video.Attr("camliPath:big.mp4"), video.Attr(nodeattr.CamliContentImage))
	}
	if rt := child("tweets", testRetweet); rt.Attr(nodeattr.Latitude) != "37.7" || rt.Attr(nodeattr.Longitude) != "-122.4" {
		t.Errorf("geotagged tweet at %q, %q", rt.Attr(nodeattr.Latitude), rt.Attr(nodeattr.Longitude))
	}

	like := child("likes", "1584364413070761985")
	if like.Attr(nodeattr.Type) != nodeTypeLike || like.Attr(nodeattr.Content) != "liked & such" || like.Attr(nodeattr.StartDate) != "2022-10-24T02:01:26.8Z" {
		t.Errorf("like = %v %q %q", like.Attr(nodeattr.Type), like.Attr(nodeattr.Content), like.Attr(nodeattr.StartDate))
	}
	if old := child("likes", "1234567"); old.Attr(nodeattr.Content) != "an old one" || old.Attr(nodeattr.StartDate) != "" {
		t.Errorf("like of an old tweet = %q, dated %q; want undated", old.Attr(nodeattr.Content), old.Attr(nodeattr.StartDate))
	}

	profile := child("profile")
	if profile.Attr(nodeattr.Type) != nodeTypeProfile || profile.Attr(nodeattr.Title) != "Brad Fitzpatrick (@bradfitz)" || profile.Attr(nodeattr.Description) != "my bio" ||
		profile.Attr(nodeattr.CamliContentImage) == "" || profile.Attr("camliPath:header") == "" {
		t.Errorf("profile = %v %q %q, avatar %q, header %q", profile.Attr(nodeattr.Type), profile.Attr(nodeattr.Title), profile.Attr(nodeattr.Description),
			profile.Attr(nodeattr.CamliContentImage), profile.Attr("camliPath:header"))
	}
	if f := child("following", "12"); f.Attr(nodeattr.Type) != nodeTypeFollow || f.Attr(attrUserID) != "12" {
		t.Errorf("follow = %v %q", f.Attr(nodeattr.Type), f.Attr(attrUserID))
	}
	if acct.Attr(acctAttrFollowersCount) != "2" || acct.Attr(acctAttrFollowerJS) == "" {
		t.Errorf("account's followers = %q, file %q", acct.Attr(acctAttrFollowersCount), acct.Attr(acctAttrFollowerJS))
	}
	if acct.Attr(importer.AcctAttrCompletedVersion) != "" {
		t.Error("archive run set the account's completed version, which is of its API runs")
	}
	if last.ItemsTotal != 6 || last.ItemsDone != 6 || last.WarningCount != 0 {
		t.Errorf("progress = %+v; want all 6 items done, without warnings", last)
	}
	var bars []string
	for _, st := range rc.Progress().Snapshot() {
		bars = append(bars, fmt.Sprintf("%s %d/%d skipped %d %v", st.Name, st.Done, st.Total, st.Skipped, st.Finished))
	}
	if got, want := strings.Join(bars, ", "), "tweets 3/3 skipped 0 true, likes 2/2 skipped 0 true, follows 1/1 skipped 0 true"; got != want {
		t.Errorf("bars = %q; want %q", got, want)
	}

	// Again: all unchanged.
	if err := im.importArchive(rc, src, names, report); err != nil {
		t.Fatal(err)
	}
	if last.ItemsDone != 6 || last.ItemsUnchanged != 6 {
		t.Errorf("after a second run, progress = %+v; want its 6 items unchanged", last)
	}
}
