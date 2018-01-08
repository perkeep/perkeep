// Copyright 2016 Google Inc. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package storage contains a Google Cloud Storage client.
//
// This package is experimental and may make backwards-incompatible changes.
package storage

import (
	"errors"
	"fmt"
	"reflect"
	"unicode/utf8"

	"golang.org/x/net/context"
	raw "google.golang.org/api/storage/v1"
)

// CopierFrom creates a Copier that can copy src to dst.
// You can immediately call Run on the returned Copier, or
// you can configure it first.
func (dst *ObjectHandle) CopierFrom(src *ObjectHandle) *Copier {
	return &Copier{dst: dst, src: src}
}

// A Copier copies a source object to a destination.
type Copier struct {
	// ObjectAttrs are optional attributes to set on the destination object.
	// Any attributes must be initialized before any calls on the Copier. Nil
	// or zero-valued attributes are ignored.
	ObjectAttrs

	// RewriteToken can be set before calling Run to resume a copy
	// operation. After Run returns a non-nil error, RewriteToken will
	// have been updated to contain the value needed to resume the copy.
	RewriteToken string

	// ProgressFunc can be used to monitor the progress of a multi-RPC copy
	// operation. If ProgressFunc is not nil and CopyFrom requires multiple
	// calls to the underlying service (see
	// https://cloud.google.com/storage/docs/json_api/v1/objects/rewrite), then
	// ProgressFunc will be invoked after each call with the number of bytes of
	// content copied so far and the total size in bytes of the source object.
	//
	// ProgressFunc is intended to make upload progress available to the
	// application. For example, the implementation of ProgressFunc may update
	// a progress bar in the application's UI, or log the result of
	// float64(copiedBytes)/float64(totalBytes).
	//
	// ProgressFunc should return quickly without blocking.
	ProgressFunc func(copiedBytes, totalBytes uint64)

	dst, src *ObjectHandle
}

// Run performs the copy.
func (c *Copier) Run(ctx context.Context) (*ObjectAttrs, error) {
	// TODO(jba): add ObjectHandle.validate to do these checks.
	if c.src.bucket == "" || c.dst.bucket == "" {
		return nil, errors.New("storage: the source and destination bucket names must both be non-empty")
	}
	if c.src.object == "" || c.dst.object == "" {
		return nil, errors.New("storage: the source and destination object names must both be non-empty")
	}
	if !utf8.ValidString(c.src.object) {
		return nil, fmt.Errorf("storage: object name %q is not valid UTF-8", c.src.object)
	}
	if !utf8.ValidString(c.dst.object) {
		return nil, fmt.Errorf("storage: dst name %q is not valid UTF-8", c.dst.object)
	}
	var rawObject *raw.Object
	// If any attribute was set, then we make sure the name matches the destination
	// name, and we check that ContentType is non-empty so we can provide a better
	// error message than the service.
	if !reflect.DeepEqual(c.ObjectAttrs, ObjectAttrs{}) {
		c.ObjectAttrs.Name = c.dst.object
		if c.ObjectAttrs.ContentType == "" {
			return nil, errors.New("storage: Copier.ContentType must be non-empty")
		}
		rawObject = c.ObjectAttrs.toRawObject(c.dst.bucket)
	}
	for {
		res, err := c.callRewrite(ctx, c.src, rawObject)
		if err != nil {
			return nil, err
		}
		if c.ProgressFunc != nil {
			c.ProgressFunc(res.TotalBytesRewritten, res.ObjectSize)
		}
		if res.Done { // Finished successfully.
			return newObject(res.Resource), nil
		}
	}
	return nil, nil
}

func (c *Copier) callRewrite(ctx context.Context, src *ObjectHandle, rawObj *raw.Object) (*raw.RewriteResponse, error) {
	call := c.dst.c.raw.Objects.Rewrite(src.bucket, src.object, c.dst.bucket, c.dst.object, rawObj)

	call.Context(ctx).Projection("full")
	if c.RewriteToken != "" {
		call.RewriteToken(c.RewriteToken)
	}
	if err := applyConds("Copy destination", c.dst.conds, call); err != nil {
		return nil, err
	}
	if err := applyConds("Copy source", toSourceConds(c.src.conds), call); err != nil {
		return nil, err
	}
	res, err := call.Do()
	if err != nil {
		return nil, err
	}
	c.RewriteToken = res.RewriteToken
	return res, nil
}

// ComposerFrom creates a Composer that can compose srcs into dst.
// You can immediately call Run on the returned Composer, or you can
// configure it first.
func (dst *ObjectHandle) ComposerFrom(srcs ...*ObjectHandle) *Composer {
	return &Composer{dst: dst, srcs: srcs}
}

// A Composer composes source objects into a destination object.
type Composer struct {
	// ObjectAttrs are optional attributes to set on the destination object.
	// Any attributes must be initialized before any calls on the Composer. Nil
	// or zero-valued attributes are ignored.
	ObjectAttrs

	dst  *ObjectHandle
	srcs []*ObjectHandle
}

// Run performs the compose operation.
func (c *Composer) Run(ctx context.Context) (*ObjectAttrs, error) {
	if c.dst.bucket == "" || c.dst.object == "" {
		return nil, errors.New("storage: the destination bucket and object names must be non-empty")
	}
	if len(c.srcs) == 0 {
		return nil, errors.New("storage: at least one source object must be specified")
	}

	req := &raw.ComposeRequest{}
	if !reflect.DeepEqual(c.ObjectAttrs, ObjectAttrs{}) {
		req.Destination = c.ObjectAttrs.toRawObject(c.dst.bucket)
		req.Destination.Name = c.dst.object
	}

	for _, src := range c.srcs {
		if src.bucket != c.dst.bucket {
			return nil, fmt.Errorf("storage: all source objects must be in bucket %q, found %q", c.dst.bucket, src.bucket)
		}
		if src.object == "" {
			return nil, errors.New("storage: all source object names must be non-empty")
		}
		srcObj := &raw.ComposeRequestSourceObjects{
			Name: src.object,
		}
		if err := applyConds("ComposeFrom source", src.conds, composeSourceObj{srcObj}); err != nil {
			return nil, err
		}
		req.SourceObjects = append(req.SourceObjects, srcObj)
	}

	call := c.dst.c.raw.Objects.Compose(c.dst.bucket, c.dst.object, req).Context(ctx)
	if err := applyConds("ComposeFrom destination", c.dst.conds, call); err != nil {
		return nil, err
	}

	obj, err := call.Do()
	if err != nil {
		return nil, err
	}
	return newObject(obj), nil
}
