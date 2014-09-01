/*
Copyright 2014 The Camlistore Authors.

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

// Package allimporters registers all the importer implementations.
package allimporters

import (
	_ "camlistore.org/pkg/importer/dummy"
	_ "camlistore.org/pkg/importer/feed"
	_ "camlistore.org/pkg/importer/flickr"
	_ "camlistore.org/pkg/importer/foursquare"
	_ "camlistore.org/pkg/importer/picasa"
	_ "camlistore.org/pkg/importer/pinboard"
	_ "camlistore.org/pkg/importer/twitter"
)
