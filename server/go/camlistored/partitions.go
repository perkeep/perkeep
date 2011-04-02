/*
Copyright 2011 Google Inc.

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

package main

import (
	"regexp"

	"camli/blobserver"
)

var validPartitionPattern = regexp.MustCompile(`^[a-z0-9_]+$`)

func isValidPartitionName(name string) bool {
	return len(name) <= 50 && validPartitionPattern.MatchString(name)
}

type partitionConfig struct {
	name                      string
	writable, readable, queue bool
	mirrors                   []blobserver.Partition
	urlbase                   string
}

func (p *partitionConfig) Name() string                                { return p.name }
func (p *partitionConfig) Writable() bool                              { return p.writable }
func (p *partitionConfig) Readable() bool                              { return p.readable }
func (p *partitionConfig) IsQueue() bool                               { return p.queue }
func (p *partitionConfig) URLBase() string                             { return p.urlbase }
func (p *partitionConfig) GetMirrorPartitions() []blobserver.Partition { return p.mirrors }

