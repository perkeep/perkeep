// Package b2 provides an idiomatic, efficient interface to Backblaze B2 Cloud Storage.
//
// Uploading
//
// Uploads to B2 require a SHA1 header, so the hash of the file must be known
// before the upload starts. The (*Bucket).Upload API tries its best not to
// buffer the entire file in memory, and it will avoid it if passed either a
// bytes.Buffer or a io.ReadSeeker.
//
// If you know the SHA1 and the length of the file in advance, you can use
// (*Bucket).UploadWithSHA1 but you are responsible for retrying on
// transient errors.
//
// Downloading
//
// Downloads from B2 are simple GETs, so if you want more control than the
// standard functions you can build your own URL according to the API docs.
// All the information you need is returned by Client.LoginInfo().
//
// Hidden files and versions
//
// There is no first-class support for versions in this library, but most
// behaviors are transparently exposed.  Upload can be used multiple times
// with the same name, ListFiles will only return the latest version of
// non-hidden files, and ListFilesVersions will return all files and versions.
//
// Unsupported APIs
//
// Large files (b2_*_large_file, b2_*_part), b2_get_download_authorization,
// b2_hide_file, b2_update_bucket.
//
// Debug mode
//
// If the B2_DEBUG environment variable is set to 1, all API calls will be
// logged. On Go 1.7 and later, it will also log when new (non-reused)
// connections are established.
package b2

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
)

// Error is the decoded B2 JSON error return value. It's not the only type of
// error returned by this package, and it is mostly returned wrapped in a
// url.Error. Use UnwrapError to access it.
type Error struct {
	Code    string
	Message string
	Status  int
}

func (e *Error) Error() string {
	return fmt.Sprintf("b2 remote error [%s]: %s", e.Code, e.Message)
}

// UnwrapError attempts to extract the Error that caused err. If there is no
// Error object to unwrap, ok is false and err is nil. That does not mean that
// the original error should be ignored.
func UnwrapError(err error) (b2Err *Error, ok bool) {
	if e, ok := err.(*url.Error); ok {
		err = e.Err
	}
	if e, ok := err.(*Error); ok {
		return e, true
	}
	return nil, false
}

const (
	defaultAPIURL = "https://api.backblaze.com"
	apiPath       = "/b2api/v1/"
)

// LoginInfo holds the information obtained upon login, which are sufficient
// to interact with the API directly.
type LoginInfo struct {
	AccountID string
	ApiURL    string

	// DownloadURL is the base URL for file downloads. It is supposed
	// to never change for the same account.
	DownloadURL string
	// AuthorizationToken is the value to pass in the Authorization
	// header of all private calls. This is valid for at most 24 hours.
	AuthorizationToken string
}

// LoginInfo returns the LoginInfo object currently in use. If refresh is
// true, it obtains a new one before returning.
//
// Note that once you obtain this there is no guarantee on its freshness,
// and it will eventually expire.
func (c *Client) LoginInfo(refresh bool) (*LoginInfo, error) {
	if refresh {
		if err := c.login(nil); err != nil {
			return nil, err
		}
	}
	return c.loginInfo.Load().(*LoginInfo), nil
}

// A Client is an authenticated API client. It is safe for concurrent use and should
// be reused to take advantage of connection and URL reuse.
//
// The Client handles refreshing authorization tokens transparently.
type Client struct {
	accountID, applicationKey string

	loginInfo atomic.Value // *LoginInfo
	// loginMu is held to avoid multiple logins in flight at the same time
	loginMu sync.Mutex

	hc *http.Client
}

// NewClient calls b2_authorize_account and returns an authenticated Client.
// httpClient can be nil, in which case http.DefaultClient will be used.
func NewClient(accountID, applicationKey string, httpClient *http.Client) (*Client, error) {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	c := &Client{
		accountID:      accountID,
		applicationKey: applicationKey,
		hc:             httpClient,
	}

	if err := c.login(nil); err != nil {
		return nil, err
	}

	c.hc.Transport = &transport{t: c.hc.Transport, c: c}
	return c, nil
}

func (c *Client) login(failedRes *http.Response) error {
	c.loginMu.Lock()
	defer c.loginMu.Unlock()

	// check under the lock that another login didn't beat us
	if failedRes != nil && c.loginInfo.Load() != nil {
		current := c.loginInfo.Load().(*LoginInfo).AuthorizationToken
		failed := failedRes.Request.Header.Get("Authorization")
		if current != failed {
			debugf("another login call succeeded concurrently")
			return nil
		}
	}

	r, err := http.NewRequest("GET", defaultAPIURL+apiPath+"b2_authorize_account", nil)
	if err != nil {
		return err
	}
	r.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString(
		[]byte(c.accountID+":"+c.applicationKey)))

	res, err := c.hc.Do(r)
	if err != nil {
		return err
	}
	defer drainAndClose(res.Body)
	debugf("login: %d", res.StatusCode)

	if res.StatusCode != 200 {
		b2Err := &Error{}
		if err := json.NewDecoder(res.Body).Decode(b2Err); err != nil {
			return fmt.Errorf("unknown error during b2_authorize_account: %d", res.StatusCode)
		}
		return b2Err
	}

	li := &LoginInfo{}
	if err := json.NewDecoder(res.Body).Decode(li); err != nil {
		return fmt.Errorf("failed to decode b2_authorize_account answer: %s", err)
	}
	c.loginInfo.Store(li)

	return nil
}

// transport is a wrapper providing authentication, tracing and error handling.
type transport struct {
	t http.RoundTripper
	c *Client
}

// requestExtFunc is implemented in the go1.7 file, to add httptrace
var requestExtFunc func(*http.Request) *http.Request

func (t *transport) RoundTrip(req *http.Request) (res *http.Response, err error) {
	if req.Header.Get("Authorization") == "" {
		req.Header.Set("Authorization", t.c.loginInfo.Load().(*LoginInfo).AuthorizationToken)
	}

	if requestExtFunc != nil {
		req = requestExtFunc(req)
	}

	if t.t == nil {
		res, err = http.DefaultTransport.RoundTrip(req)
	} else {
		res, err = t.t.RoundTrip(req)
	}

	if err == nil && res.StatusCode != 200 {
		return nil, parseB2Error(res)
	}

	return res, err
}

var debug = os.Getenv("B2_DEBUG") == "1"

func debugf(format string, a ...interface{}) {
	if debug {
		log.Printf("[b2] "+format, a...)
	}
}

func (c *Client) doRequest(endpoint string, params map[string]interface{}) (*http.Response, error) {
	body, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	// Reduce debug log noise
	delete(params, "accountID")
	delete(params, "bucketID")

	apiURL := c.loginInfo.Load().(*LoginInfo).ApiURL
	res, err := c.hc.Post(apiURL+apiPath+endpoint, "application/json", bytes.NewBuffer(body))
	if e, ok := UnwrapError(err); ok && e.Status == http.StatusUnauthorized {
		if err = c.login(res); err == nil {
			res, err = c.hc.Post(apiURL+apiPath+endpoint, "application/json", bytes.NewBuffer(body))
		}
	}
	if err != nil {
		debugf("%s (%v): %v", endpoint, params, err)
	} else {
		debugf("%s (%v)", endpoint, params)
	}
	return res, err
}

func parseB2Error(res *http.Response) error {
	defer drainAndClose(res.Body)
	b2Err := &Error{}
	if err := json.NewDecoder(res.Body).Decode(b2Err); err != nil {
		return fmt.Errorf("unknown error during b2_authorize_account: %d", res.StatusCode)
	}
	return b2Err
}

// drainAndClose will make an attempt at flushing and closing the body so that the
// underlying connection can be reused.  It will not read more than 10KB.
func drainAndClose(body io.ReadCloser) {
	io.CopyN(ioutil.Discard, body, 10*1024)
	body.Close()
}

// A Bucket is bound to the Client that created it. It is safe for concurrent use and
// should be reused to take advantage of connection and URL reuse.
type Bucket struct {
	ID string
	c  *Client

	uploadURLs   []*uploadURL
	uploadURLsMu sync.Mutex
}

// BucketInfo is an extended Bucket object with metadata.
type BucketInfo struct {
	Bucket

	Name string
	Type string
}

// BucketByID returns a Bucket bound to the Client. It does NOT check that the
// bucket actually exists, or perform any network operation.
func (c *Client) BucketByID(id string) *Bucket {
	return &Bucket{ID: id, c: c}
}

// BucketByName returns the Bucket with the given name. If such a bucket is not
// found and createIfNotExists is true, CreateBucket is called with allPublic set
// to false. Otherwise, an error is returned.
func (c *Client) BucketByName(name string, createIfNotExists bool) (*BucketInfo, error) {
	bs, err := c.Buckets()
	if err != nil {
		return nil, err
	}
	for _, b := range bs {
		if b.Name == name {
			return b, nil
		}
	}
	if !createIfNotExists {
		return nil, errors.New("bucket not found: " + name)
	}
	return c.CreateBucket(name, false)
}

// Buckets returns a list of buckets sorted by name.
func (c *Client) Buckets() ([]*BucketInfo, error) {
	res, err := c.doRequest("b2_list_buckets", map[string]interface{}{
		"accountId": c.accountID,
	})
	if err != nil {
		return nil, err
	}
	defer drainAndClose(res.Body)
	var buckets struct {
		Buckets []struct {
			BucketID, BucketName, BucketType string
		}
	}
	if err := json.NewDecoder(res.Body).Decode(&buckets); err != nil {
		return nil, err
	}
	var r []*BucketInfo
	for _, b := range buckets.Buckets {
		r = append(r, &BucketInfo{
			Bucket: Bucket{
				ID: b.BucketID,
				c:  c,
			},
			Name: b.BucketName,
			Type: b.BucketType,
		})
	}
	return r, nil
}

// CreateBucket creates a bucket with b2_create_bucket. If allPublic is true,
// files in this bucket can be downloaded by anybody.
func (c *Client) CreateBucket(name string, allPublic bool) (*BucketInfo, error) {
	bucketType := "allPrivate"
	if allPublic {
		bucketType = "allPublic"
	}
	res, err := c.doRequest("b2_create_bucket", map[string]interface{}{
		"accountId":  c.accountID,
		"bucketName": name,
		"bucketType": bucketType,
	})
	if err != nil {
		return nil, err
	}
	defer drainAndClose(res.Body)
	var bucket struct {
		BucketID string
	}
	if err := json.NewDecoder(res.Body).Decode(&bucket); err != nil {
		return nil, err
	}
	return &BucketInfo{
		Bucket: Bucket{
			c: c, ID: bucket.BucketID,
		},
		Name: name,
		Type: bucketType,
	}, nil
}

// Delete calls b2_delete_bucket. After this call succeeds the Bucket object
// becomes invalid and any other calls will fail.
func (b *Bucket) Delete() error {
	res, err := b.c.doRequest("b2_delete_bucket", map[string]interface{}{
		"accountId": b.c.accountID,
		"bucketId":  b.ID,
	})
	if err != nil {
		return err
	}
	drainAndClose(res.Body)
	return nil
}
