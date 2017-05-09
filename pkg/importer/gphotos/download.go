/*
Copyright 2017 The Camlistore Authors

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

package gphotos

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/time/rate"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"
)

var scopeURLs = []string{drive.DrivePhotosReadonlyScope}

const (
	// maximum number of results returned per response page
	batchSize = 1000

	// defaultRateLimit is the request rate limiting we start with at the beginning of an importer run.
	// It is the default value for the drive API (that can be adjusted in the developers console):
	// 1000 queries/100 seconds/user.
	// The rate limiting is then dynamically adjusted during the importer run.
	defaultRateLimit = rate.Limit(10)
)

// getUser helper function to return the user's email address.
func getUser(ctx context.Context, client *http.Client) (*drive.User, error) {
	srv, err := drive.New(client)
	if err != nil {
		return nil, err
	}
	about, err := srv.About.Get().
		Context(ctx).
		Fields("user(displayName,emailAddress,permissionId)").Do()
	if err != nil {
		return nil, err
	}
	return about.User, nil
}

type downloader struct {
	// download rate limiter
	rate *rate.Limiter

	*drive.Service
}

// newDownloader returns a downloader with the given http.Client
// to download photos.
//
// The client must be authenticated for drive.DrivePhotosReadonlyScope
// ("https://www.googleapis.com/auth/drive.photos.readonly")..
func newDownloader(client *http.Client) (*downloader, error) {
	srv, err := drive.New(client)
	if err != nil {
		return nil, err
	}
	return &downloader{
		rate:    rate.NewLimiter(defaultRateLimit, 1),
		Service: srv,
	}, nil
}

// photos returns a channel which will receive all the photos metadata
// in batches.
//
// If sinceToken is provided, only photos modified or created after sinceToken are sent.
// Typically, sinceToken is empty on the first importer run,
// and the returned token is saved by the importer,
// to be passed as the sinceToken in the next photos() call.
//
// Returns a new token to watch future changes.
func (dl *downloader) photos(ctx context.Context, sinceToken string) (photosCh <-chan maybePhotos, nextToken string, err error) {

	// reset the rate limiter
	dl.rate.SetLimit(defaultRateLimit)

	var sr *drive.StartPageToken
	if err := dl.rateLimit(ctx, func() error {
		var err error
		sr, err = dl.Service.Changes.GetStartPageToken().Do()
		return err
	}); err != nil {
		return nil, "", err
	}
	nextToken = sr.StartPageToken

	ch := make(chan maybePhotos, 1)
	photosCh = ch
	if sinceToken != "" {
		go dl.getChanges(ctx, ch, sinceToken)
	} else {
		go dl.getPhotos(ctx, ch)
	}

	return photosCh, nextToken, nil
}

const fields = "id,name,mimeType,description,starred,properties,version,webContentLink,createdTime,modifiedTime,originalFilename,imageMediaMetadata(location,time)"

// getPhotos sends all photos found on drive to ch.
// It returns when all found photos were sent, or when an error occurs, or when it gets cancelled.
// It does not close ch.
func (dl *downloader) getPhotos(ctx context.Context, ch chan<- maybePhotos) {
	var n int64
	defer func() {
		close(ch)
		logf("received a total of %d files.", n)
	}()

	listCall := dl.Service.Files.List().
		Fields("nextPageToken, files(" + fields + ")").
		// If users ran the Picasa importer and they hit the 10000 images limit
		// bug, they're missing their most recent photos, so we start by importing
		// the most recent ones, since they should already have the oldest ones.
		// However, https://developers.google.com/drive/v3/reference/files/list
		// states OrderBy does not work for > 1e6 files.
		OrderBy("createdTime desc,folder").
		Spaces("photos").
		PageSize(batchSize)

	var token string
	for {
		select {
		case <-ctx.Done():
			ch <- maybePhotos{err: ctx.Err()}
			return
		default:
		}

		listTokenCall := listCall.PageToken(token)
		var r *drive.FileList
		if err := dl.rateLimit(ctx, func() error {
			var err error
			r, err = listTokenCall.Context(ctx).Do()
			return err
		}); err != nil {
			ch <- maybePhotos{err: err}
			return
		}

		logf("receiving %d files.", len(r.Files))
		photos := make([]photo, 0, len(r.Files))
		for _, f := range r.Files {
			if f != nil {
				photos = append(photos, dl.fileAsPhoto(f))
			}
		}
		n += int64(len(photos))
		ch <- maybePhotos{photos: photos}

		if token = r.NextPageToken; token == "" {
			return
		}
	}
}

// getChanges sends to ch all photos modified or created after sinceToken.
// It returns after all found photos were sent, or when an error occurs, or when it gets cancelled.
// It does not close ch.
func (dl *downloader) getChanges(ctx context.Context, ch chan<- maybePhotos, sinceToken string) {
	var n int64
	defer func() {
		close(ch)
		logf("received a total of %d changes.", n)
	}()

	token := sinceToken
	for {
		select {
		case <-ctx.Done():
			ch <- maybePhotos{err: ctx.Err()}
			return
		default:
		}

		var r *drive.ChangeList
		if err := dl.rateLimit(ctx, func() error {
			logf("importing changes at revision %v", token)
			var err error
			r, err = dl.Service.Changes.List(token).
				Context(ctx).
				Fields("nextPageToken,newStartPageToken, changes(file(" + fields + "))").
				Spaces("photos").
				PageSize(batchSize).
				RestrictToMyDrive(true).
				IncludeRemoved(false).Do()
			return err
		}); err != nil {
			ch <- maybePhotos{err: err}
			return
		}
		photos := make([]photo, 0, len(r.Changes))
		for _, c := range r.Changes {
			if c.File != nil {
				photos = append(photos, dl.fileAsPhoto(c.File))
			}
		}
		n += int64(len(photos))
		if len(photos) > 0 {
			ch <- maybePhotos{photos: photos}
		}
		if token = r.NextPageToken; token == "" {
			return
		}
	}
	return
}

// maybePhotos contains the photos found in the response to a drive list call,
// and the last error to occur, if any, when getting these photos.
type maybePhotos struct {
	photos []photo
	err    error
}

type photo struct {
	ID                          string
	Name, MimeType, Description string
	Starred                     bool
	Properties                  map[string]string
	WebContentLink              string
	CreatedTime, ModifiedTime   time.Time
	OriginalFilename            string
	Version                     int64
	drive.FileImageMediaMetadata
}

func (dl *downloader) openPhoto(ctx context.Context, photo photo) (io.ReadCloser, error) {
	logf("importing media from %v", photo.WebContentLink)
	var resp *http.Response
	err := dl.rateLimit(ctx, func() error {
		var err error
		resp, err = dl.Service.Files.Get(photo.ID).Context(ctx).Download()
		return err
	})
	if err != nil {
		return nil, err
	}
	return resp.Body, err
}

// fileAsPhoto returns a photo populated with the information found about file.
//
// The returned photo contains only the metadata;
// the content of the photo can be downloaded with dl.openPhoto.
func (dl *downloader) fileAsPhoto(f *drive.File) photo {
	if f == nil {
		return photo{}
	}
	p := photo{
		ID:               f.Id,
		Name:             f.Name,
		Starred:          f.Starred,
		Version:          f.Version,
		MimeType:         f.MimeType,
		Properties:       f.Properties,
		Description:      f.Description,
		WebContentLink:   f.WebContentLink,
		OriginalFilename: f.OriginalFilename,
	}
	if f.ImageMediaMetadata != nil {
		p.FileImageMediaMetadata = *f.ImageMediaMetadata
	}
	if f.CreatedTime != "" {
		p.CreatedTime, _ = time.Parse(time.RFC3339, f.CreatedTime)
	}
	if f.ModifiedTime != "" {
		p.ModifiedTime, _ = time.Parse(time.RFC3339, f.ModifiedTime)
	}

	return p
}

// rateLimit calls f obeying the global Rate limit.
// On "Rate Limit Exceeded" error, the rate is throttled back,
// on success the limit is raised.
func (dl *downloader) rateLimit(ctx context.Context, f func() error) error {
	const (
		msgRateLimitExceeded          = "Rate Limit Exceeded"
		msgUserRateLimitExceeded      = "User Rate Limit Exceeded"
		msgUserRateLimitExceededShort = "userRateLimitExceeded"
	)

	var err error
	first := true
	// Ensure a 1 minute try limit.
	ctx, _ = context.WithTimeout(ctx, time.Minute)
	for {
		now := time.Now()
		if err := dl.rate.Wait(ctx); err != nil {
			return err
		}
		// The scheduler may interrupt here, but we don't know anything better, so risk it.
		dur := time.Since(now)
		if err = f(); err == nil {
			if first {
				// If we're limited by the rate, then raise the limit!
				lim := dl.rate.Limit()
				// We're rate limited iff we wait at least the rate limit time:
				if dur >= time.Duration(float64(time.Second)/float64(lim)) {
					// to reach 1 again after a halving, it is
					//   * 7 steps with 1.1,
					//   * 3 steps with 1.25,
					//   * 2 steps with 1.5.
					dl.rate.SetLimit(lim * 1.1)
				}
			}
			return nil
		}
		first = false
		ge, ok := err.(*googleapi.Error)
		if !ok || ge.Code != 403 {
			return err
		}
		if ge.Message != "" &&
			ge.Message != msgRateLimitExceeded &&
			ge.Message != msgUserRateLimitExceeded {
			return err
		}
		if ge.Message == "" {
			var ok bool
			for _, e := range ge.Errors {
				if ok = e.Reason == msgUserRateLimitExceededShort; ok {
					break
				}
			}
			// For some cases, googleapi does not parse the returned JSON
			// properly, so we have to fall back to check the original text.
			//
			// Usually this is a "User Rate Limit Exceeded", but that's
			// also a "Rate Limit Exceeded", and we're interested just in the
			// fact, not the cause.
			if !ok && !strings.Contains(ge.Body, msgRateLimitExceeded) {
				return err
			}
		}

		dl.rate.SetLimit(dl.rate.Limit() / 2) // halve the rate (exponential backoff)
	}
	return err
}
