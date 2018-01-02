/*
Copyright 2014 The Perkeep Authors.

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
package allimporters // import "perkeep.org/pkg/importer/allimporters"

import (
	_ "perkeep.org/pkg/importer/dummy"
	_ "perkeep.org/pkg/importer/feed"
	_ "perkeep.org/pkg/importer/flickr"
	_ "perkeep.org/pkg/importer/foursquare"
	_ "perkeep.org/pkg/importer/gphotos"
	_ "perkeep.org/pkg/importer/picasa"
	_ "perkeep.org/pkg/importer/pinboard"
	_ "perkeep.org/pkg/importer/plaid"
	_ "perkeep.org/pkg/importer/twitter"
)
