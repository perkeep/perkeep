/*
Copyright 2014 The Perkeep Authors

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

// The pk-deploy program deploys Perkeep on cloud computing platforms
// such as Google Compute Engine or Amazon EC2.
package main // import "perkeep.org/cmd/pk-deploy"

import (
	"perkeep.org/pkg/cmdmain"
)

func main() {
	cmdmain.Main()
}
