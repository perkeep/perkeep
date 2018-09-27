/*
Copyright 2018 The Perkeep Authors.

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

// Package importshare provides a method to import blobs shared from another
// Perkeep server.
package importshare

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"perkeep.org/internal/httputil"
	"perkeep.org/pkg/auth"
	"perkeep.org/pkg/client"
	"perkeep.org/pkg/types/camtypes"

	"golang.org/x/net/context/ctxhttp"
)

const refreshPeriod = 2 * time.Second // how often to refresh the import dialog in the web UI

// Import sends the shareURL to the server, so it can import all the blobs
// transitively reachable through the claim in that share URL. It then regularly
// polls the server to get the state of the currently running import process, and
// it uses updateDialogFunc to update the web UI with that state. Message is
// printed everytime the dialog updates, and importedBlobRef, if not nil, is used
// at the end of a successful import to create a link to the newly imported file or
// directory.
func Import(ctx context.Context, config map[string]string, shareURL string,
	updateDialogFunc func(message string, importedBlobRef string)) {
	printerr := func(msg string) {
		showError(msg, func(msg string) {
			updateDialogFunc(msg, "")
		})
	}
	if config == nil {
		printerr("Nil config for Import share")
		return
	}

	authToken, ok := config["authToken"]
	if !ok {
		printerr("No authToken in config for Import share")
		return
	}
	importSharePrefix, ok := config["importShare"]
	if !ok {
		printerr("No importShare in config for Import share")
		return
	}

	am, err := auth.TokenOrNone(authToken)
	if err != nil {
		printerr(fmt.Sprintf("Error with authToken: %v", err))
		return
	}
	cl, err := client.New(client.OptionAuthMode(am))
	if err != nil {
		printerr(fmt.Sprintf("Error with client initialization: %v", err))
		return
	}
	go func() {
		if err := cl.Post(ctx, importSharePrefix, "application/x-www-form-urlencoded",
			strings.NewReader(url.Values{"shareurl": {shareURL}}.Encode())); err != nil {
			printerr(err.Error())
			return
		}
		for {
			select {
			case <-ctx.Done():
				printerr(ctx.Err().Error())
				return
			case <-time.After(refreshPeriod):
				var progress camtypes.ShareImportProgress
				res, err := ctxhttp.Get(ctx, cl.HTTPClient(), importSharePrefix)
				if err != nil {
					printerr(err.Error())
					continue
				}
				if err := httputil.DecodeJSON(res, &progress); err != nil {
					printerr(err.Error())
					continue
				}
				updateDialog(progress, updateDialogFunc)
				if !progress.Running {
					return
				}
			}
		}
	}()
}

// updateDialog uses updateDialogFunc to refresh the dialog that displays the
// status of the import. Message is printed first in the dialog, and
// importBlobRef is only passed when the import is done, to be displayed below as a
// link to the newly imported file or directory.
func updateDialog(progress camtypes.ShareImportProgress,
	updateDialogFunc func(message string, importedBlobRef string)) {
	if progress.Running {
		if progress.Assembled {
			updateDialogFunc("Importing file in progress", "")
			return
		}
		updateDialogFunc(fmt.Sprintf("Working - %d/%d files imported", progress.FilesCopied, progress.FilesSeen), "")
		return
	}

	if progress.Assembled {
		updateDialogFunc(fmt.Sprintf("File successfully imported as"), progress.BlobRef.String())
		return
	}
	updateDialogFunc(fmt.Sprintf("Done - %d/%d files imported under", progress.FilesCopied, progress.FilesSeen), progress.BlobRef.String())
}

func showError(message string, updateDialogFunc func(string)) {
	println(message)
	updateDialogFunc(message)
}
