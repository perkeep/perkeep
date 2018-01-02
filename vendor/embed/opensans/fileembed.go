/*
Copyright 2016 The Perkeep Authors

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
Package opensans provides access to the Font Awesome font library and
embeds them into the Go binary when compiled with the genfileembed
tool.

See https://www.fontsquirrel.com/fonts/open-sans

#fileembed pattern .*\.(css|eot|svg|ttf|woff|otf)$
*/
package opensans

import "perkeep.org/pkg/fileembed"

var Files = &fileembed.Files{}
