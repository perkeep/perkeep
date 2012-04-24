// +build !windows

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

package webserver

import (
	"fmt"
	"log"
	"os"
	"strconv"
)

func pipeFromEnvFd(env string) (*os.File, error) {
	fdStr := os.Getenv(env)
	if fdStr == "" {
		return nil, fmt.Errorf("Environment variable %q was blank", env)
	}
	fd, err := strconv.Atoi(fdStr)
	if err != nil {
		log.Fatalf("Bogus test harness fd '%s': %v", fdStr, err)
	}
	return os.NewFile(uintptr(fd), "testingpipe-"+env), nil
}
