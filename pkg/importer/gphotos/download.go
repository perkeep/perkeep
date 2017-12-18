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
	"errors"
	"io"
	"log"
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

// getUser returns the authenticated Google Drive user's User value,
// containing their name, email address, and "permission ID",
// which is the "The user's ID as visible in Permission resources" according
// to https://developers.google.com/drive/v3/reference/about#resource
// The permission ID becomes the "userID" (AcctAttrUserID) value on the
// account's "importerAccount" permanode.
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
	// rate is the download rate limiter.
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

// foreachPhoto runs fn on each photo. If f returns an error, iteration
// stops with that error.
//
// If sinceToken is provided, only photos modified or created after sinceToken are sent.
// Typically, sinceToken is empty on the first importer run,
// and the returned token is saved by the importer,
// to be passed as the sinceToken in the next photos() call.
//
// Returns a new token to watch future changes.
func (dl *downloader) foreachPhoto(ctx context.Context, sinceToken string, fn func(context.Context, photo) error) (nextToken string, err error) {

	if sinceToken != "" {
		return dl.foreachPhotoFromChanges(ctx, sinceToken, fn)
	}

	// Get a start page token *before* we enumerate the world, so
	// if there are changes during the import, we won't miss
	// anything.
	var sr *drive.StartPageToken
	if err := dl.rateLimit(ctx, func() error {
		var err error
		sr, err = dl.Service.Changes.GetStartPageToken().Do()
		return err
	}); err != nil {
		return "", err
	}
	nextToken = sr.StartPageToken
	if nextToken == "" {
		return "", errors.New("unexpected gdrive Changes.GetStartPageToken response with empty StartPageToken")
	}

	if err := dl.foreachPhotoFromScratch(ctx, fn); err != nil {
		return "", err
	}
	return nextToken, nil
}

const fields = "id,name,mimeType,description,starred,properties,version,webContentLink,createdTime,modifiedTime,originalFilename,imageMediaMetadata(location,time)"

func (dl *downloader) foreachPhotoFromScratch(ctx context.Context, fn func(context.Context, photo) error) error {
	var token string
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		var r *drive.FileList
		if err := dl.rateLimit(ctx, func() error {
			var err error
			listCall := dl.Service.Files.List().
				Context(ctx).
				Fields("nextPageToken, files(" + fields + ")").
				// If users ran the Picasa importer and they hit the 10000 images limit
				// bug, they're missing their most recent photos, so we start by importing
				// the most recent ones, since they should already have the oldest ones.
				// However, https://developers.google.com/drive/v3/reference/files/list
				// states OrderBy does not work for > 1e6 files.
				OrderBy("createdTime desc,folder").
				Spaces("photos").
				PageSize(batchSize).
				PageToken(token)
			r, err = listCall.Do()
			return err
		}); err != nil {
			return err
		}

		logf("got gdrive API response of batch of %d files", len(r.Files))
		for _, f := range r.Files {
			if f == nil {
				// Can this happen? Was in the code before.
				logf("unexpected nil entry in gdrive file list response")
				continue
			}
			if err := fn(ctx, dl.fileAsPhoto(f)); err != nil {
				return err
			}
		}
		token = r.NextPageToken
		if token == "" {
			return nil
		}
	}
}

func (dl *downloader) foreachPhotoFromChanges(ctx context.Context, sinceToken string, fn func(context.Context, photo) error) (nextToken string, err error) {
	token := sinceToken
	for {
		select {
		case <-ctx.Done():
			return "", err
		default:
		}

		var r *drive.ChangeList
		if err := dl.rateLimit(ctx, func() error {
			logf("importing changes from token point %q", token)
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
			return "", err
		}
		for _, c := range r.Changes {
			if c.File == nil {
				// Can this happen? Was in the code before.
				logf("unexpected nil entry in gdrive changes response")
				continue
			}
			if err := fn(ctx, dl.fileAsPhoto(c.File)); err != nil {
				return "", err
			}
		}
		token = r.NextPageToken
		if token == "" {
			nextToken = r.NewStartPageToken
			if nextToken == "" {
				return "", errors.New("unexpected gdrive changes response with both NextPageToken and NewStartPageToken empty")
			}
			return nextToken, nil
		}
	}
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
// On "Rate Limit Exceeded" error, it sleeps and tries later.
func (dl *downloader) rateLimit(ctx context.Context, f func() error) error {
	const (
		msgRateLimitExceeded          = "Rate Limit Exceeded"
		msgUserRateLimitExceeded      = "User Rate Limit Exceeded"
		msgUserRateLimitExceededShort = "userRateLimitExceeded"
	)

	// Ensure a 1 minute try limit.
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	for {
		if err := dl.rate.Wait(ctx); err != nil {
			log.Printf("gphotos: rate limit failure: %v", err)
			return err
		}
		err := f()
		if err == nil {
			return nil
		}
		ge, ok := err.(*googleapi.Error)
		if !ok || ge.Code != 403 {
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
		// Some arbitrary sleep.
		log.Printf("gphotos: sleeping for 5s after 403 error, presumably due to a rate limit")
		time.Sleep(5 * time.Second)
		log.Printf("gphotos: retrying after sleep...")
	}
}
