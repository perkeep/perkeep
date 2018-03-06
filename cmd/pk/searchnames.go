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

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"

	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/client"
	"perkeep.org/pkg/cmdmain"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/search"
)

type searchNamesGetCmd struct{}
type searchNamesSetCmd struct{}

func init() {
	osutil.AddSecretRingFlag()
	cmdmain.RegisterMode("named-search-get", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		return new(searchNamesGetCmd)
	})
	cmdmain.RegisterMode("named-search-set", func(flags *flag.FlagSet) cmdmain.CommandRunner {
		return new(searchNamesSetCmd)
	})
}

func (c *searchNamesGetCmd) Describe() string { return "Get a named search's current value" }
func (c *searchNamesSetCmd) Describe() string { return "Create or update a named search" }

func (c *searchNamesGetCmd) Usage() {
	fmt.Fprintln(os.Stderr, "pk named-search-get <name>")
}
func (c *searchNamesSetCmd) Usage() {
	fmt.Fprintln(os.Stderr, "pk named-search-set <name> <new-search-expression>")
}

func (c *searchNamesGetCmd) RunCommand(args []string) error {
	if len(args) != 1 {
		return cmdmain.UsageError("expected 1 argument")
	}
	named := args[0]
	gnr, err := getNamedSearch(named)
	if err != nil {
		return err
	}
	out, err := json.MarshalIndent(gnr, "  ", "")
	if err != nil {
		return err
	}
	fmt.Fprintln(cmdmain.Stdout, string(out))
	return nil
}

func (c *searchNamesSetCmd) RunCommand(args []string) error {
	if len(args) != 2 {
		return cmdmain.UsageError("expected 2 arguments")
	}
	named, substitute := args[0], args[1]

	cc := newClient("")
	uh := client.NewUploadHandleFromString(substitute)
	substpr, err := cc.Upload(ctxbg, uh)
	if err != nil {
		return err
	}
	var pn blob.Ref
	claims := []*schema.Builder{}
	gr, err := getNamedSearch(named)
	if err == nil {
		pn = gr.PermaRef
	} else {
		pnpr, err := cc.UploadAndSignBlob(ctxbg, schema.NewUnsignedPermanode())
		if err != nil {
			return err
		}
		pn = pnpr.BlobRef

		claims = append(claims, schema.NewSetAttributeClaim(pn, "camliNamedSearch", named))
		claims = append(claims, schema.NewSetAttributeClaim(pn, "title", fmt.Sprintf("named:%s", named)))
	}
	claims = append(claims, schema.NewSetAttributeClaim(pn, "camliContent", substpr.BlobRef.String()))
	for _, claimBuilder := range claims {
		_, err := cc.UploadAndSignBlob(ctxbg, claimBuilder)
		if err != nil {
			return err
		}
	}
	snr := setNamedResponse{PermaRef: pn, SubstRef: substpr.BlobRef}
	out, err := json.MarshalIndent(snr, "  ", "")
	if err != nil {
		return err
	}
	fmt.Fprintln(cmdmain.Stdout, string(out))
	return nil
}

type getNamedResponse struct {
	Named      string   `json:"named"`
	Substitute string   `json:"substitute"`
	PermaRef   blob.Ref `json:"permaRef"`
	SubstRef   blob.Ref `json:"substRef"`
}

type setNamedResponse struct {
	PermaRef blob.Ref `json:"permaRef"`
	SubstRef blob.Ref `json:"substRef"`
}

func getNamedSearch(named string) (getNamedResponse, error) {
	cc := newClient("")
	var gnr getNamedResponse
	gnr.Named = named
	sr, err := cc.Query(ctxbg, search.NamedSearch(named))
	if err != nil {
		return gnr, err
	}
	if len(sr.Blobs) < 1 {
		return gnr, fmt.Errorf("No named search found for: %s", named)
	}
	gnr.PermaRef = sr.Blobs[0].Blob
	substRefS := sr.Describe.Meta.Get(gnr.PermaRef).Permanode.Attr.Get("camliContent")
	br, ok := blob.Parse(substRefS)
	if !ok {
		return gnr, fmt.Errorf("Invalid blob ref: %s", substRefS)
	}
	reader, _, err := cc.Fetch(ctxbg, br)
	if err != nil {
		return gnr, err
	}
	result, err := ioutil.ReadAll(reader)
	if err != nil {
		return gnr, err
	}
	gnr.Substitute = string(result)
	return gnr, nil
}
