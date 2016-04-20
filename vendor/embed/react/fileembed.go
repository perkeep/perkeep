/*
Copyright 2013 Google Inc.

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
Package react provides access to the React JavaScript libraries and
embeds them into the Go binary when compiled with the genfileembed
tool.

See http://facebook.github.io/react/

#fileembed pattern .*\.js$
*/
package react

import "camlistore.org/pkg/fileembed"

var Files = &fileembed.Files{}
