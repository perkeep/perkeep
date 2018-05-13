/*
Copyright 2012 The Perkeep Authors

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

package serverinit

import (
	"go4.org/jsonconfig"
)

var GenLowLevelConfig = genLowLevelConfig

var DefaultBaseConfig = defaultBaseConfig

func (c *Config) Export_Obj() jsonconfig.Obj { return c.jconf }

func ExportNewConfigFromObj(obj jsonconfig.Obj) *Config {
	return &Config{jconf: obj}
}

func SetTempDirFunc(f func() string) {
	tempDir = f
}

func SetNoMkdir(v bool) {
	noMkdir = v
}

func ConfigHandler(c *Config) configHandler {
	return configHandler{c}
}
