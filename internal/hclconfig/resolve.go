package hclconfig

import (
	"errors"
	"fmt"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
)

/*
TODO: this is kinda messy, clean it up eventually. The idea behind this is that we want to
resolve all inline handlers by adding them to the prefixes under a unique prefix and replace
them in the object by a reference to that newly added prefix. The benefit of this is that we
can write code in `schema.go` as if inline handlers would not exist and eventually reuse it
in the handler factories.
*/

func resolveInlineStorages(obj cty.Value, parentPrefix string, prefixes map[string]lowLevelPrefix) (cty.Value, hcl.Diagnostics) {
	var diags hcl.Diagnostics

	paths, moreDiags := inlineStoragePaths(obj)
	diags = append(diags, moreDiags...)
	if diags.HasErrors() {
		return cty.EmptyObjectVal, diags
	}

	res, err := cty.Transform(obj, func(p cty.Path, v cty.Value) (cty.Value, error) {
		if !paths.Has(p) {
			return v, nil
		}
		if !v.Type().IsObjectType() {
			return v, nil
		}
		inlinePrefix := parentPrefix
		for _, ps := range p {
			var k string
			switch tps := ps.(type) {
			case cty.GetAttrStep:
				k = tps.Name
			case cty.IndexStep:
				k = tps.Key.AsBigFloat().String()
			}
			inlinePrefix = fmt.Sprintf("/%s/%s/", strings.Trim(inlinePrefix, "/"), k)
		}

		resolved, moreDiags := resolveInlineStorages(v, inlinePrefix, prefixes)
		diags = append(diags, moreDiags...)
		if diags.HasErrors() {
			return cty.EmptyObjectVal, errors.New(diags.Error())
		}

		storageType, moreDiags := typeFromObject(resolved)
		diags = append(diags, moreDiags...)
		if diags.HasErrors() {
			return cty.EmptyObjectVal, errors.New(diags.Error())
		}

		inlineStorageArgs, moreDiags := storageArgs(resolved)
		diags = append(diags, moreDiags...)
		if diags.HasErrors() {
			return cty.EmptyObjectVal, errors.New(diags.Error())
		}

		prefixes[inlinePrefix] = lowLevelPrefix{
			Handler:     fmt.Sprintf("storage-%s", storageType),
			HandlerArgs: inlineStorageArgs,
		}

		return cty.StringVal(inlinePrefix), nil
	})

	if err != nil {
		return cty.EmptyObjectVal, diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Unable to resolve inline storages",
			Detail:   fmt.Sprintf("Error when resolving inline storages: %s", err),
		})
	}

	return res, diags
}
