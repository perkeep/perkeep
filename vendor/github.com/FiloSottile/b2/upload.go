package b2

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
)

// Upload uploads a file to a B2 bucket. If mimeType is "", "b2/x-auto" will be used.
//
// Concurrent calls to Upload will use separate upload URLs, but consequent ones
// will attempt to reuse previously obtained ones to save b2_get_upload_url calls.
// Upload URL failures are handled transparently.
//
// Since the B2 API requires a SHA1 header, normally the file will first be read
// entirely into a memory buffer. Two cases avoid the memory copy: if r is a
// bytes.Buffer, the SHA1 will be computed in place; otherwise, if r implements io.Seeker
// (like *os.File and *bytes.Reader), the file will be read twice, once to compute
// the SHA1 and once to upload.
//
// If a file by this name already exist, a new version will be created.
func (b *Bucket) Upload(r io.Reader, name, mimeType string) (*FileInfo, error) {
	var body io.ReadSeeker
	switch r := r.(type) {
	case *bytes.Buffer:
		defer r.Reset() // we are expected to consume it
		body = bytes.NewReader(r.Bytes())
	case io.ReadSeeker:
		body = r
	default:
		b, err := ioutil.ReadAll(r)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(b)
	}

	h := sha1.New()
	length, err := io.Copy(h, body)
	if err != nil {
		return nil, err
	}
	sha1Sum := hex.EncodeToString(h.Sum(nil))

	var fi *FileInfo
	for i := 0; i < 5; i++ {
		if _, err = body.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}

		fi, err = b.UploadWithSHA1(body, name, mimeType, sha1Sum, length)
		if err == nil {
			break
		}
	}
	return fi, err
}

type uploadURL struct {
	UploadURL, AuthorizationToken string
}

func (b *Bucket) getUploadURL() (u *uploadURL, err error) {
	b.uploadURLsMu.Lock()
	if len(b.uploadURLs) > 0 {
		u = b.uploadURLs[len(b.uploadURLs)-1]
		b.uploadURLs = b.uploadURLs[:len(b.uploadURLs)-1]
	}
	b.uploadURLsMu.Unlock()
	if u != nil {
		return
	}

	res, err := b.c.doRequest("b2_get_upload_url", map[string]interface{}{
		"bucketId": b.ID,
	})
	if err != nil {
		return
	}
	defer drainAndClose(res.Body)
	err = json.NewDecoder(res.Body).Decode(&u)
	return
}

func (b *Bucket) putUploadURL(u *uploadURL) {
	b.uploadURLsMu.Lock()
	defer b.uploadURLsMu.Unlock()
	b.uploadURLs = append(b.uploadURLs, u)
}

// UploadWithSHA1 is like Upload, but allows the caller to specify previously
// known SHA1 and length of the file. It never does any buffering, nor does it
// retry on failure.
//
// Note that retrying on most upload failures is mandatory by the B2 API
// documentation, and not just error condition handling.
//
// sha1Sum should be the hex encoding of the SHA1 sum of what will be read from r.
//
// This is an advanced interface, most clients should use Upload, and consider
// passing it a bytes.Buffer or io.ReadSeeker to avoid buffering.
func (b *Bucket) UploadWithSHA1(r io.Reader, name, mimeType, sha1Sum string, length int64) (*FileInfo, error) {
	uurl, err := b.getUploadURL()
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", uurl.UploadURL, ioutil.NopCloser(r))
	if err != nil {
		return nil, err
	}
	req.ContentLength = length
	req.Header.Set("Authorization", uurl.AuthorizationToken)
	req.Header.Set("X-Bz-File-Name", url.QueryEscape(name))
	req.Header.Set("Content-Type", mimeType)
	req.Header.Set("X-Bz-Content-Sha1", sha1Sum)

	res, err := b.c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != 200 {
		return nil, parseB2Error(res)
	}
	defer drainAndClose(res.Body)

	fi := fileInfoObj{}
	if err = json.NewDecoder(res.Body).Decode(&fi); err != nil {
		return nil, err
	}
	b.putUploadURL(uurl)
	return fi.makeFileInfo(), nil
}
