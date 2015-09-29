/*
Copyright 2014 The Camlistore Authors

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

/*
The publisher application serves and renders items published by Camlistore.
That is, items that are children, through a (direct or not) camliPath relation,
of a camliRoot node (a permanode with a camliRoot attribute set).

#fileembed pattern .+\.(js|css|html|png|svg)$
*/
package main

import (
	"camlistore.org/pkg/fileembed"
)

// TODO(mpl): appengine case

var Files = &fileembed.Files{}
