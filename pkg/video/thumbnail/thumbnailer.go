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

package thumbnail

import (
	"io"
	"net/url"
	"os/exec"

	"go4.org/jsonconfig"
)

// Thumbnailer is the interface that wraps the Command method.
//
// Command receives the (HTTP) uri from where to get the video to generate a
// thumbnail and returns program and arguments.
// The command is expected to output the thumbnail image on its stdout, or exit
// with an error code.
//
// See FFmpegThumbnailer.Command for example.
type Thumbnailer interface {
	Command(*url.URL) (prog string, args []string)
}

// DefaultThumbnailer is the default Thumbnailer when no config is set.
var DefaultThumbnailer Thumbnailer = FFmpegThumbnailer{}

// FFmpegThumbnailer is a Thumbnailer that generates a thumbnail with ffmpeg.
type FFmpegThumbnailer struct{}

var _ Thumbnailer = (*FFmpegThumbnailer)(nil)

// Command implements the Command method for the Thumbnailer interface.
func (f FFmpegThumbnailer) Command(uri *url.URL) (string, []string) {
	return "ffmpeg", []string{
		"-seekable", "1",
		"-i", uri.String(),
		"-vf", "thumbnail",
		"-frames:v", "1",
		"-f", "image2pipe",
		"-c:v", "png",
		"pipe:1",
	}
}

type configThumbnailer struct {
	prog string
	args []string
}

var _ Thumbnailer = (*configThumbnailer)(nil)

func (ct *configThumbnailer) Command(uri *url.URL) (string, []string) {
	args := make([]string, len(ct.args))
	for index, arg := range ct.args {
		if arg == "$uri" {
			args[index] = uri.String()
		} else {
			args[index] = arg
		}
	}
	return ct.prog, args
}

func buildCmd(tn Thumbnailer, uri *url.URL, out io.Writer) *exec.Cmd {
	prog, args := tn.Command(uri)
	cmd := exec.Command(prog, args...)
	cmd.Stdout = out
	return cmd
}

func thumbnailerFromConfig(config jsonconfig.Obj) Thumbnailer {
	command := config.OptionalList("command")
	if len(command) < 1 {
		return DefaultThumbnailer
	}
	return &configThumbnailer{prog: command[0], args: command[1:]}
}
