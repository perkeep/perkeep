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
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"go4.org/syncutil"
	"golang.org/x/sync/errgroup"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/importer"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/schema/nodeattr"
	"perkeep.org/pkg/types/camtypes"
)

// An account's archive, which X makes on request, as a ZIP of JavaScript
// files of JSON ("data/tweets.js" and so on, listed by "data/manifest.js"),
// and of the tweets' media ("data/tweets_media/<tweet ID>-<name>"). Older
// archives, of 2013 to about 2018, have the tweets in monthly files
// ("data/js/tweets/YYYY_MM.js"), without their media.
const (
	maxArchiveRecord = 1 << 20 // of a small file, such as account.js
	maxArchiveData   = 1 << 30 // of a file of tweets, likes or follows

	nodeTypeFollow  = "twitter.com:follow"
	nodeTypeProfile = "twitter.com:profile"

	attrUserID = "twitterUserId" // of the account a follow is of

	// Of the account node: the archive's files of its follows and
	// followers, which are of their accounts' IDs only, and the
	// followers' count.
	acctAttrFollowingJS    = "twitterFollowingJS"
	acctAttrFollowerJS     = "twitterFollowerJS"
	acctAttrFollowersCount = "twitterFollowersCount"
)

// memberSource is an archive's ZIP, as importer.Archive is, or a test's.
type memberSource interface {
	Open(string) (io.ReadCloser, error)
	OpenLimit(string, int64) (io.ReadCloser, error)
}

var _ importer.ArchiveImporter = (*imp)(nil)

// members are the sizes of an archive's files, by name.
type members map[string]int64

func (m members) has(name string) bool {
	_, ok := m[name]
	return ok
}

// withExt returns the member name, or else one of name with an extension,
// as the files of the profile's header images have, though their URLs
// don't.
func (m members) withExt(name string) (member string, ok bool) {
	if m.has(name) {
		return name, true
	}
	for n := range m {
		if ext := strings.TrimPrefix(n, name); ext != n && strings.HasPrefix(ext, ".") && !strings.Contains(ext[1:], ".") {
			return n, true
		}
	}
	return "", false
}

func archiveNames(a *importer.Archive) members {
	names := make(members, len(a.Zip.File))
	for _, f := range a.Zip.File {
		if !f.FileInfo().IsDir() {
			names[f.Name] = int64(f.UncompressedSize64)
		}
	}
	return names
}

func (*imp) InspectArchive(ctx context.Context, a *importer.Archive) (camtypes.ArchiveIdentity, error) {
	return inspectArchive(a, archiveNames(a))
}

func inspectArchive(src memberSource, names members) (camtypes.ArchiveIdentity, error) {
	switch {
	case names.has("data/account.js"):
		var accts []struct {
			Account struct {
				AccountID   string `json:"accountId"`
				Username    string `json:"username"`
				DisplayName string `json:"accountDisplayName"`
				Email       string `json:"email"`
			} `json:"account"`
		}
		if err := readJS(src, "data/account.js", maxArchiveRecord, &accts); err != nil {
			return camtypes.ArchiveIdentity{}, err
		}
		if len(accts) != 1 || accts[0].Account.AccountID == "" {
			return camtypes.ArchiveIdentity{}, errors.New("twitter: archive's account.js is of no one account")
		}
		a := accts[0].Account
		return camtypes.ArchiveIdentity{UserID: a.AccountID, UserName: a.Username, Name: a.DisplayName, Email: a.Email}, nil
	case names.has("data/js/user_details.js"):
		var u struct {
			ID         string `json:"id"`
			ScreenName string `json:"screen_name"`
			FullName   string `json:"full_name"`
		}
		if err := readJS(src, "data/js/user_details.js", maxArchiveRecord, &u); err != nil {
			return camtypes.ArchiveIdentity{}, err
		}
		if u.ID == "" {
			return camtypes.ArchiveIdentity{}, errors.New("twitter: archive's user_details.js has no user ID")
		}
		return camtypes.ArchiveIdentity{UserID: u.ID, UserName: u.ScreenName, Name: u.FullName}, nil
	}
	return camtypes.ArchiveIdentity{}, errors.New("twitter: not a Twitter archive: it has neither data/account.js nor data/js/user_details.js")
}

func (im *imp) RunArchive(rc *importer.RunContext, a *importer.Archive) error {
	return im.importArchive(rc, a, archiveNames(a), func(p camtypes.ArchiveImportProgress) error {
		return rc.SetArchiveProgress(p)
	})
}

// archiveRun is the state of a run of an archive.
type archiveRun struct {
	*run
	src    memberSource
	names  members
	report func(camtypes.ArchiveImportProgress) error

	mu       sync.Mutex // guards progress
	progress camtypes.ArchiveImportProgress
}

// importArchive imports the archive src, of the members names, reporting
// its progress with report.
func (im *imp) importArchive(rc *importer.RunContext, src memberSource, names members, report func(camtypes.ArchiveImportProgress) error) error {
	r := &archiveRun{run: &run{RunContext: rc, im: im}, src: src, names: names, report: report}
	var err error
	if names.has("data/manifest.js") {
		err = r.importModern()
	} else {
		err = r.importOld()
	}
	if err != nil {
		return err
	}
	if err := rc.Context().Err(); err != nil {
		return err
	}
	return r.notify()
}

// item counts an item of the archive done, and whether it was unchanged,
// reporting the progress every hundred items.
func (r *archiveRun) item(unchanged bool) error {
	r.mu.Lock()
	r.progress.ItemsDone++
	if unchanged {
		r.progress.ItemsUnchanged++
	}
	every := r.progress.ItemsDone%100 == 0
	r.mu.Unlock()
	if every {
		return r.notify()
	}
	return nil
}

func (r *archiveRun) warn(format string, args ...any) {
	r.Logf(format, args...)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.progress.WarningCount++
	if len(r.progress.Warnings) < 100 {
		r.progress.Warnings = append(r.progress.Warnings, fmt.Sprintf(format, args...))
	}
}

func (r *archiveRun) notify() error {
	r.mu.Lock()
	p := r.progress
	p.Warnings = slices.Clone(p.Warnings)
	r.mu.Unlock()
	return r.report(p)
}

// archiveManifest is of an archive's data/manifest.js: the files of its
// kinds of data, and how many items each has.
type archiveManifest struct {
	DataTypes map[string]struct {
		Files []struct {
			FileName string `json:"fileName"`
			Count    string `json:"count"`
		} `json:"files"`
	} `json:"dataTypes"`
}

// files returns the files of the data type typ, and how many items they
// have, or else, if the manifest doesn't list it, the file def, if the
// archive has it.
func (m *archiveManifest) files(names members, typ, def string) (files []string, count int64) {
	for _, f := range m.DataTypes[typ].Files {
		files = append(files, f.FileName)
		n, _ := strconv.ParseInt(f.Count, 10, 64)
		count += n
	}
	if len(files) == 0 && names.has(def) {
		files = []string{def}
	}
	return files, count
}

func (r *archiveRun) importModern() error {
	var m archiveManifest
	if err := readJS(r.src, "data/manifest.js", maxArchiveData, &m); err != nil {
		return err
	}
	tweetFiles, nTweets := m.files(r.names, "tweets", "data/tweets.js")
	likeFiles, nLikes := m.files(r.names, "like", "data/like.js")
	followFiles, nFollows := m.files(r.names, "following", "data/following.js")
	// The total is reported with the items' progress, after the bars of
	// the run's steps are made, which show it better than one bar of all.
	r.progress.ItemsTotal = int(nTweets + nLikes + nFollows)
	if err := r.importArchiveTweets(tweetFiles, nTweets); err != nil {
		return err
	}
	if err := r.importLikes(likeFiles, nLikes); err != nil {
		return err
	}
	if err := r.importProfile(); err != nil {
		return err
	}
	if err := r.importFollows(followFiles, nFollows); err != nil {
		return err
	}
	return r.attachFollowFiles()
}

// archiveTweet is a tweet of an archive's tweets.js.
type archiveTweet struct {
	Id           string `json:"id_str"`
	FullText     string `json:"full_text"`
	CreatedAtStr string `json:"created_at"` // as time.RubyDate

	// One or both might be present, of numbers as strings:
	Geo         *archiveCoords `json:"geo"`         // lat, long
	Coordinates *archiveCoords `json:"coordinates"` // geojson: long, lat

	Entities struct {
		URLs []*urlEntity `json:"urls"`
	} `json:"entities"`
	ExtendedEntities struct {
		Media []*archiveTweetMedia `json:"media"`
	} `json:"extended_entities"`

	media []tweetMedia // of the archive, as the run read them
}

func (t *archiveTweet) ID() string        { return t.Id }
func (t *archiveTweet) CreatedAt() string { return t.CreatedAtStr }
func (t *archiveTweet) Text() string      { return html.UnescapeString(t.FullText) }
func (t *archiveTweet) Liked() bool       { return false }

func (t *archiveTweet) LatLong() (lat, long float64, ok bool) {
	var g *geo
	var c *coords
	if t.Geo != nil {
		g = &geo{t.Geo.floats()}
	}
	if t.Coordinates != nil {
		c = &coords{t.Coordinates.floats()}
	}
	return latLong(g, c)
}

// archiveCoords are an archive's coordinates of a tweet, which it writes
// as strings of numbers.
type archiveCoords struct {
	Coordinates []jsonFloat `json:"coordinates"`
}

func (c *archiveCoords) floats() []float64 {
	fs := make([]float64, len(c.Coordinates))
	for i, f := range c.Coordinates {
		fs[i] = float64(f)
	}
	return fs
}

// jsonFloat is a number of JSON, as a number or a string.
type jsonFloat float64

func (f *jsonFloat) UnmarshalJSON(b []byte) error {
	v, err := strconv.ParseFloat(strings.Trim(string(b), `"`), 64)
	if err != nil {
		return err
	}
	*f = jsonFloat(v)
	return nil
}

func (t *archiveTweet) Media() []tweetMedia {
	return append(t.media[:len(t.media):len(t.media)], getImagesFromURLs(t.Entities.URLs)...)
}

// archiveTweetMedia is a photo, video or GIF of a tweet of an archive.
type archiveTweetMedia struct {
	MediaURLHTTPS string `json:"media_url_https"` // of a photo, or a video's or GIF's preview image
	Type          string `json:"type"`            // "photo", "video" or "animated_gif"
	VideoInfo     struct {
		Variants []struct {
			Bitrate     string `json:"bitrate"`
			ContentType string `json:"content_type"`
			URL         string `json:"url"`
		} `json:"variants"`
	} `json:"video_info"`
}

// name returns the name of the media's file: of a photo, as the API's
// media are named; of a video or GIF, of its best MP4.
func (m *archiveTweetMedia) name() string {
	if m.Type == "photo" {
		return path.Base(m.MediaURLHTTPS)
	}
	best, bestRate := "", -1
	for _, v := range m.VideoInfo.Variants {
		if v.ContentType != "video/mp4" {
			continue
		}
		rate, _ := strconv.Atoi(v.Bitrate)
		if rate > bestRate {
			u, _, _ := strings.Cut(v.URL, "?")
			best, bestRate = path.Base(u), rate
		}
	}
	return best
}

// storedMedia is a medium of an archive's tweet, as its file in the
// archive, stored by the run if the tweet's node lacks it.
type storedMedia struct {
	name  string
	photo bool
	ref   blob.Ref // of the stored file, or zero if the node has it
}

func (m *storedMedia) URLs() []string       { return nil }
func (m *storedMedia) BaseFilename() string { return m.name }

// Archives' items are imported in batches of up to archiveBatchItems, or of
// up to archiveBatchMedia bytes of tweets' media, read from the archive in
// its order, as that's faster, before the batch's imports, which store
// them, many at once. A medium of more bytes is read as it's stored.
const (
	archiveBatchItems = 100
	archiveBatchMedia = 32 << 20
	maxBufferedMedium = 16 << 20
)

// importBatch imports the items of the children of parent of paths, a
// batch: attrs returns the attributes to set of each item, given its node,
// tweetsAtOnce at a time; they're set with as few claims as hold them;
// and then done is called with each item, and whether it changed.
func (r *archiveRun) importBatch(parent *importer.Object, paths []string, attrs func(i int, n *importer.Object) ([]string, error), done func(i int, changed bool) error) error {
	if len(paths) == 0 {
		return nil
	}
	nodes, err := parent.ChildPathObjects(r.Context(), paths)
	if err != nil {
		return err
	}
	claims := r.Host.NewClaimBatch()
	g, _ := errgroup.WithContext(r.Context())
	g.SetLimit(tweetsAtOnce)
	for i, n := range nodes {
		g.Go(func() error {
			kv, err := attrs(i, n)
			if err == nil && len(kv) > 0 {
				claims.SetAttrs(n, kv...)
			}
			return err
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}
	if err := claims.Commit(r.Context()); err != nil {
		return err
	}
	for i, n := range nodes {
		if err := done(i, claims.Changed(n)); err != nil {
			return err
		}
	}
	return nil
}

// importArchiveTweets imports the tweets of the archive's tweets files,
// of count tweets, and their media in the archive.
func (r *archiveRun) importArchiveTweets(files []string, count int64) error {
	tweetsNode, err := r.getTopLevelNode("tweets")
	if err != nil {
		return err
	}
	bar := r.Progress().Bar("tweets", "Importing tweets")
	bar.SetTotal(count)
	var (
		sawTweet   = map[string]bool{}
		batch      []*archiveTweet
		batchMedia [][]tweetMedium
		batchBytes int64
	)
	flush := func() error {
		ids := make([]string, len(batch))
		for i, t := range batch {
			ids[i] = t.Id
		}
		err := r.importBatch(tweetsNode, ids, func(i int, node *importer.Object) ([]string, error) {
			t := batch[i]
			if err := r.storeTweetMedia(node, t, batchMedia[i]); err != nil {
				return nil, err
			}
			attrs, err := r.tweetAttrs(node, t, false)
			if err != nil {
				r.errorf("error importing tweet %s: %v", t.Id, err)
			}
			return attrs, err
		}, func(i int, changed bool) error {
			if changed {
				bar.Add(1)
			} else {
				bar.Skip(1)
			}
			return r.item(!changed)
		})
		batch, batchMedia, batchBytes = batch[:0], batchMedia[:0], 0
		return err
	}
	for _, file := range files {
		err := readJSArray(r.src, file, maxArchiveData, func(e struct {
			Tweet *archiveTweet `json:"tweet"`
		}) error {
			t := e.Tweet
			if t == nil || t.Id == "" || sawTweet[t.Id] {
				return nil
			}
			sawTweet[t.Id] = true
			if err := r.Context().Err(); err != nil {
				return err
			}
			media, n, err := r.readTweetMedia(t)
			if err != nil {
				return err
			}
			batch, batchMedia, batchBytes = append(batch, t), append(batchMedia, media), batchBytes+n
			if len(batch) == archiveBatchItems || batchBytes >= archiveBatchMedia {
				return flush()
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("twitter: reading %s: %w", file, err)
		}
	}
	if err := flush(); err != nil {
		return err
	}
	if err := r.Context().Err(); err != nil {
		return err
	}
	bar.Finish()
	return nil
}

// tweetMedium is a medium of an archive's tweet, as its member of the
// archive, and its bytes, if they were read ahead of being stored.
type tweetMedium struct {
	m      *storedMedia
	member string
	data   []byte
}

// readTweetMedia returns the media of the archive's tweet t in the archive,
// reading those of up to maxBufferedMedium bytes, and how many bytes it
// read.
func (r *archiveRun) readTweetMedia(t *archiveTweet) (media []tweetMedium, read int64, err error) {
	for _, m := range t.ExtendedEntities.Media {
		name := m.name()
		if name == "" {
			continue
		}
		member := "data/tweets_media/" + t.Id + "-" + name
		n, ok := r.names[member]
		if !ok {
			r.warn("tweet %s's %s isn't in the archive", t.Id, name)
			continue
		}
		tm := tweetMedium{m: &storedMedia{name: name, photo: m.Type == "photo"}, member: member}
		if n <= maxBufferedMedium {
			if tm.data, err = r.readMember(member, n); err != nil {
				return nil, 0, err
			}
			read += n
		}
		media = append(media, tm)
	}
	return media, read, nil
}

func (r *archiveRun) readMember(member string, size int64) ([]byte, error) {
	rd, err := r.src.OpenLimit(member, size)
	if err != nil {
		return nil, err
	}
	defer rd.Close()
	data, err := io.ReadAll(rd)
	if err != nil {
		return nil, fmt.Errorf("twitter: reading %s: %w", member, err)
	}
	return data, nil
}

// storeTweetMedia stores the media of the archive's tweet t, of its node,
// that the node lacks, and sets t's media to them.
func (r *archiveRun) storeTweetMedia(node *importer.Object, t *archiveTweet, media []tweetMedium) error {
	for _, tm := range media {
		if node.Attr("camliPath:"+tm.m.name) == "" {
			var ref blob.Ref
			var err error
			if tm.data != nil {
				ref, err = schema.WriteFileFromReader(r.Context(), r.Host.Target(), tm.m.name, bytes.NewReader(tm.data))
			} else {
				ref, err = r.storeMember(tm.member, tm.m.name)
			}
			if err != nil {
				return fmt.Errorf("twitter: storing %s: %w", tm.member, err)
			}
			tm.m.ref = ref
		}
		t.media = append(t.media, tm.m)
	}
	return nil
}

// storeMember stores the archive's member as a file named name.
func (r *archiveRun) storeMember(member, name string) (blob.Ref, error) {
	rd, err := r.src.Open(member)
	if err != nil {
		return blob.Ref{}, err
	}
	defer rd.Close()
	ref, err := schema.WriteFileFromReader(r.Context(), r.Host.Target(), name, rd)
	if err != nil {
		return blob.Ref{}, fmt.Errorf("twitter: storing %s: %w", member, err)
	}
	return ref, nil
}

// snowflakeEpoch is when Twitter's IDs, which since November 2010 have
// had the milliseconds since it in their high bits, start.
const snowflakeEpoch = 1288834974657

// tweetTime returns when the tweet of id was posted, if its ID says,
// as those since November 2010 do.
func tweetTime(id string) (time.Time, bool) {
	n, err := strconv.ParseInt(id, 10, 64)
	// The IDs before them were sequential, and less than 30 billion.
	if err != nil || n < 1e12 {
		return time.Time{}, false
	}
	return time.UnixMilli(n>>22 + snowflakeEpoch).UTC(), true
}

// archiveLike is a like of an archive's like.js.
type archiveLike struct {
	TweetID     string `json:"tweetId"`
	FullText    string `json:"fullText"`
	ExpandedURL string `json:"expandedUrl"`
}

// importLikes imports the archive's likes, of count likes. An archive's
// likes have only the liked tweets' IDs, texts and URLs, so they're
// dated by when the tweets were posted, as their IDs say.
func (r *archiveRun) importLikes(files []string, count int64) error {
	if len(files) == 0 {
		return nil
	}
	likesNode, err := r.getTopLevelNode("likes")
	if err != nil {
		return err
	}
	bar := r.Progress().Bar("likes", "Importing likes")
	bar.SetTotal(count)
	var (
		saw   = map[string]bool{}
		batch []archiveLike
	)
	flush := func() error {
		ids := make([]string, len(batch))
		for i, l := range batch {
			ids[i] = l.TweetID
		}
		err := r.importBatch(likesNode, ids, func(i int, n *importer.Object) ([]string, error) {
			l := batch[i]
			if n.Attr(attrImportMethod) == "api" {
				return nil, nil
			}
			attrs := []string{
				"twitterId", l.TweetID,
				nodeattr.Type, nodeTypeLike,
				nodeattr.Content, html.UnescapeString(l.FullText),
				nodeattr.URL, l.ExpandedURL,
				attrImportMethod, "zip",
			}
			if t, ok := tweetTime(l.TweetID); ok {
				attrs = append(attrs, nodeattr.StartDate, schema.RFC3339FromTime(t))
			}
			return attrs, nil
		}, func(i int, changed bool) error {
			if changed {
				bar.Add(1)
			} else {
				bar.Skip(1)
			}
			return r.item(!changed)
		})
		batch = batch[:0]
		return err
	}
	for _, file := range files {
		err := readJSArray(r.src, file, maxArchiveData, func(e struct {
			Like archiveLike `json:"like"`
		}) error {
			l := e.Like
			if l.TweetID == "" || saw[l.TweetID] {
				return nil
			}
			saw[l.TweetID] = true
			if err := r.Context().Err(); err != nil {
				return err
			}
			if batch = append(batch, l); len(batch) == archiveBatchItems {
				return flush()
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("twitter: reading %s: %w", file, err)
		}
	}
	if err := flush(); err != nil {
		return err
	}
	if err := r.Context().Err(); err != nil {
		return err
	}
	bar.Finish()
	return nil
}

// importProfile imports the account's profile, of its profile.js and its
// images in profile_media, as a "profile" child of the root.
func (r *archiveRun) importProfile() error {
	if !r.names.has("data/profile.js") {
		return nil
	}
	var profiles []struct {
		Profile struct {
			Description struct {
				Bio      string `json:"bio"`
				Website  string `json:"website"`
				Location string `json:"location"`
			} `json:"description"`
			AvatarMediaURL string `json:"avatarMediaUrl"`
			HeaderMediaURL string `json:"headerMediaUrl"`
		} `json:"profile"`
	}
	if err := readJS(r.src, "data/profile.js", maxArchiveRecord, &profiles); err != nil {
		return err
	}
	if len(profiles) == 0 {
		return nil
	}
	p := profiles[0].Profile
	acct := r.AccountNode()
	n, err := r.RootNode().ChildPathObject("profile")
	if err != nil {
		return err
	}
	title := "@" + acct.Attr(importer.AcctAttrUserName)
	if name := acct.Attr(importer.AcctAttrName); name != "" {
		title = name + " (" + title + ")"
	}
	attrs := []string{
		nodeattr.Type, nodeTypeProfile,
		nodeattr.Title, title,
		nodeattr.Description, p.Description.Bio,
		nodeattr.URL, p.Description.Website,
		"twitterLocation", p.Description.Location,
	}
	userID := acct.Attr(importer.AcctAttrUserID)
	for _, img := range []struct{ url, path string }{{p.AvatarMediaURL, "avatar"}, {p.HeaderMediaURL, "header"}} {
		if img.url == "" {
			continue
		}
		name := path.Base(img.url)
		member, ok := r.names.withExt("data/profile_media/" + userID + "-" + name)
		if !ok {
			r.warn("profile's %s, %s, isn't in the archive", img.path, name)
			continue
		}
		ref, err := r.storeMember(member, path.Base(member))
		if err != nil {
			return err
		}
		attrs = append(attrs, "camliPath:"+img.path, ref.String())
		if img.path == "avatar" {
			attrs = append(attrs, nodeattr.CamliContentImage, ref.String())
		}
	}
	return n.SetAttrs(attrs...)
}

// archiveFollow is a follow of an archive's following.js.
type archiveFollow struct {
	AccountID string `json:"accountId"`
	UserLink  string `json:"userLink"`
}

// importFollows imports the accounts the account follows, of count
// follows, as children of a "following" child of the root. An archive has
// only their IDs and links.
func (r *archiveRun) importFollows(files []string, count int64) error {
	if len(files) == 0 {
		return nil
	}
	followingNode, err := r.RootNode().ChildPathObject("following")
	if err != nil {
		return err
	}
	if err := followingNode.SetAttr(nodeattr.Title, fmt.Sprintf("%s's follows", r.AccountNode().Attr(importer.AcctAttrUserName))); err != nil {
		return err
	}
	bar := r.Progress().Bar("follows", "Importing follows")
	bar.SetTotal(count)
	var (
		saw   = map[string]bool{}
		batch []archiveFollow
	)
	flush := func() error {
		ids := make([]string, len(batch))
		for i, f := range batch {
			ids[i] = f.AccountID
		}
		err := r.importBatch(followingNode, ids, func(i int, n *importer.Object) ([]string, error) {
			f := batch[i]
			return []string{
				nodeattr.Type, nodeTypeFollow,
				nodeattr.Title, "Follow of account " + f.AccountID,
				attrUserID, f.AccountID,
				nodeattr.URL, f.UserLink,
			}, nil
		}, func(i int, changed bool) error {
			if changed {
				bar.Add(1)
			} else {
				bar.Skip(1)
			}
			return r.item(!changed)
		})
		batch = batch[:0]
		return err
	}
	for _, file := range files {
		err := readJSArray(r.src, file, maxArchiveData, func(e struct {
			Following archiveFollow `json:"following"`
		}) error {
			f := e.Following
			if f.AccountID == "" || saw[f.AccountID] {
				return nil
			}
			saw[f.AccountID] = true
			if err := r.Context().Err(); err != nil {
				return err
			}
			if batch = append(batch, f); len(batch) == archiveBatchItems {
				return flush()
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("twitter: reading %s: %w", file, err)
		}
	}
	if err := flush(); err != nil {
		return err
	}
	if err := r.Context().Err(); err != nil {
		return err
	}
	bar.Finish()
	return nil
}

// attachFollowFiles attaches the archive's files of the account's follows
// and followers to its node, and the followers' count, as they're of the
// accounts' IDs only, too many to each have a node.
func (r *archiveRun) attachFollowFiles() error {
	var attrs []string
	for _, f := range []struct{ member, attr string }{
		{"data/following.js", acctAttrFollowingJS},
		{"data/follower.js", acctAttrFollowerJS},
	} {
		if !r.names.has(f.member) {
			continue
		}
		ref, err := r.storeMember(f.member, path.Base(f.member))
		if err != nil {
			return err
		}
		attrs = append(attrs, f.attr, ref.String())
	}
	if r.names.has("data/follower.js") {
		n := 0
		if err := readJSArray(r.src, "data/follower.js", maxArchiveData, func(json.RawMessage) error {
			n++
			return nil
		}); err != nil {
			return err
		}
		attrs = append(attrs, acctAttrFollowersCount, strconv.Itoa(n))
	}
	if len(attrs) == 0 {
		return nil
	}
	return r.AccountNode().SetAttrs(attrs...)
}

// importOld imports the tweets of an older archive, of monthly files of
// tweets, whose media are fetched by their URLs, as the archive lacks them.
func (r *archiveRun) importOld() error {
	var files []string
	for name := range r.names {
		if strings.HasPrefix(name, "data/js/tweets/2") && strings.HasSuffix(name, ".js") {
			files = append(files, name)
		}
	}
	slices.Sort(files)
	tweetsNode, err := r.getTopLevelNode("tweets")
	if err != nil {
		return err
	}
	// The archive has a file of tweets per month, whose count is known
	// before any are read, unlike the count of their tweets. So the bar
	// counts files, each done once all of its tweets are, and its
	// description counts tweets.
	bar := r.Progress().Bar("archive", "Importing tweets from the archive")
	bar.SetTotal(int64(len(files)))
	var (
		gate     = syncutil.NewGate(tweetsAtOnce)
		grp      syncutil.Group
		sawTweet = map[string]bool{}

		mu                    sync.Mutex // guards the counts of tweets done
		tweetsDone, unchanged int
	)
	for _, file := range files {
		if err := r.Context().Err(); err != nil {
			grp.Err() // wait for the started imports
			return err
		}
		var tweets []*zipTweetItem
		if err := readJS(r.src, file, maxArchiveData, &tweets); err != nil {
			grp.Err() // wait for the started imports
			return fmt.Errorf("twitter: reading %s: %w", file, err)
		}
		for _, tweet := range tweets {
			if tweet.Id == "" || sawTweet[tweet.Id] {
				continue
			}
			sawTweet[tweet.Id] = true
			gate.Start()
			grp.Go(func() error {
				defer gate.Done()
				dup, err := r.importTweet(tweetsNode, tweet, false)
				if err != nil {
					return err
				}
				mu.Lock()
				tweetsDone++
				if dup {
					unchanged++
				}
				bar.SetDescription(fmt.Sprintf("%d tweets, %d unchanged", tweetsDone, unchanged))
				mu.Unlock()
				return r.item(dup)
			})
		}
		bar.Add(1)
	}
	if err := grp.Err(); err != nil {
		return err
	}
	if err := r.Context().Err(); err != nil {
		return err
	}
	bar.Finish()
	return nil
}

// readJS decodes the JSON of the archive's JavaScript file name, of an
// assignment of it, such as "window.YTD.account.part0 = [ ... ]", into v.
func readJS(src memberSource, name string, limit int64, v any) error {
	rd, err := src.OpenLimit(name, limit)
	if err != nil {
		return err
	}
	defer rd.Close()
	br := bufio.NewReader(rd)
	if _, err := br.ReadString('='); err != nil {
		return fmt.Errorf("twitter: %s isn't an assignment of JSON: %w", name, err)
	}
	if err := json.NewDecoder(br).Decode(v); err != nil {
		return fmt.Errorf("twitter: decoding %s: %w", name, err)
	}
	// Read the rest, so the ZIP's checksum of it is checked.
	_, err = io.Copy(io.Discard, br)
	return err
}

// readJSArray calls fn with each element of the JSON array of the
// archive's JavaScript file name, as readJS reads, decoding them as it
// goes, rather than all at once, as the files of tweets can be big.
func readJSArray[T any](src memberSource, name string, limit int64, fn func(T) error) error {
	rd, err := src.OpenLimit(name, limit)
	if err != nil {
		return err
	}
	defer rd.Close()
	br := bufio.NewReader(rd)
	if _, err := br.ReadString('='); err != nil {
		return fmt.Errorf("%s isn't an assignment of JSON: %w", name, err)
	}
	dec := json.NewDecoder(br)
	if tok, err := dec.Token(); err != nil || tok != json.Delim('[') {
		return fmt.Errorf("%s isn't of a JSON array", name)
	}
	for dec.More() {
		var v T
		if err := dec.Decode(&v); err != nil {
			return err
		}
		if err := fn(v); err != nil {
			return err
		}
	}
	if _, err := dec.Token(); err != nil {
		return err
	}
	_, err = io.Copy(io.Discard, io.MultiReader(dec.Buffered(), br))
	return err
}
