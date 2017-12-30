// Copyright 2017 Tamás Gulácsi. All rights reserved.
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package picago

import (
	"encoding/json"
	"io"
	"os"

	"golang.org/x/oauth2"
)

type FileCache struct {
	file *os.File
	ts   oauth2.TokenSource
	Log  func(...interface{}) error
}

// NewTokenCache returns a TokenSource wrapped in a file cache,
// which saves tokens into the file.
//
// ts can be nil, and later be set with SetTokenSource.
func NewTokenCache(path string, ts oauth2.TokenSource, Log func(...interface{}) error) (*FileCache, error) {
	fh, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	return &FileCache{ts: ts, file: fh, Log: Log}, nil
}
func (fc *FileCache) SetTokenSource(ts oauth2.TokenSource) {
	if fc == nil {
		return
	}
	if fc.Log != nil {
		fc.Log("msg", "SetTokenSource", ts)
	}
	fc.ts = ts
}

func (fc *FileCache) Token() (*oauth2.Token, error) {
	if fc.Log != nil {
		fc.Log("msg", "FileCache.Token")
	}
	_, _ = fc.file.Seek(0, io.SeekStart)
	var t oauth2.Token
	err := json.NewDecoder(fc.file).Decode(&t)
	if fc.Log != nil {
		fc.Log("msg", "decode", fc.file.Name(), "token", t, "error", err)
	}
	if err == nil && t.Valid() {
		return &t, nil
	}

	if fc.ts == nil {
		return nil, err
	}
	var tp *oauth2.Token
	tp, err = fc.ts.Token()
	if fc.Log != nil {
		fc.Log("msg", "pull new token from source", "token", tp, "error", err)
	}
	if err != nil {
		return tp, err
	}
	_ = fc.file.Truncate(0)
	if _, err := fc.file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}

	if err = json.NewEncoder(fc.file).Encode(t); err != nil {
		return &t, err
	}
	return &t, fc.file.Sync()
}
