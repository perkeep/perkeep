package hclconfig

import (
	"fmt"
	"path/filepath"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
)

const (
	prefixRoot     = "/"
	prefixSearch   = "/search/"
	prefixStatus   = "/status/"
	prefixSetup    = "/setup/"
	prefixHelp     = "/help/"
	prefixJsonSign = "/sighelper/"
	prefixCache    = "/cache/"
	prefixUI       = "/ui/"

	// canonical perkeepd index structure
	prefixBlobSourceAndIndex          = "/perkeepd/blobsource-and-index/"
	prefixBlobSourceAndMaybeAlsoIndex = "/perkeepd/blobsource-and-maybe-also-index/"
	prefixSyncBlobsourceToIndex       = "/perkeepd/sync-blobsource-to-index/"
	prefixIndex                       = "/perkeepd/index/"
)

type lowLevelConfig struct {
	lowLevelNetwork
	lowLevelPrefixes
}

type lowLevelNetwork struct {
	Listen     string   `json:"listen,omitempty"`
	CamliNetIP string   `json:"camliNetIP,omitempty"`
	Auth       []string `json:"auth,omitempty"`
	BaseURL    string   `json:"baseURL,omitempty"`
	HTTPS      bool     `json:"https"`
	HTTPSKey   string   `json:"httpsKey,omitempty"`
	HTTPSCert  string   `json:"httpsCert,omitempty"`
}

type lowLevelPrefixes struct {
	Prefixes map[string]lowLevelPrefix `json:"prefixes,omitempty"`
}

type lowLevelPrefix struct {
	Handler     string                 `json:"handler"`
	HandlerArgs map[string]interface{} `json:"handlerArgs,omitempty"`
}

func compileToLowLevelConfig(config Config) (lowLevelConfig, hcl.Diagnostics) {
	var diags hcl.Diagnostics

	lowLevelCfg := lowLevelConfig{
		lowLevelNetwork:  lowLevelNetwork{},
		lowLevelPrefixes: lowLevelPrefixes{Prefixes: make(map[string]lowLevelPrefix)},
	}

	diags = append(diags, addNetwork(&lowLevelCfg, config.Network, config.EvalContext)...)
	diags = append(diags, addPerkeepd(&lowLevelCfg, config.Server, config.EvalContext)...)
	for _, storage := range config.Storage {
		diags = append(diags, addStorage(&lowLevelCfg, storage, config.EvalContext)...)
	}

	return lowLevelCfg, diags
}

func addNetwork(low *lowLevelConfig, network Network, evalContext *hcl.EvalContext) hcl.Diagnostics {
	var diags hcl.Diagnostics

	low.lowLevelNetwork.Listen = network.Listen
	low.lowLevelNetwork.CamliNetIP = network.CamliNetIP
	low.lowLevelNetwork.BaseURL = network.BaseURL
	low.lowLevelNetwork.HTTPS = network.HTTPS
	low.lowLevelNetwork.HTTPSCert = network.HTTPSCert
	low.lowLevelNetwork.HTTPSKey = network.HTTPSKey

	authValues := make([]string, 0)
	for _, auth := range network.Auths {
		authVal, moreDiags := renderAuth(auth, evalContext)
		diags = append(diags, moreDiags...)
		if diags.HasErrors() {
			continue
		}
		authValues = append(authValues, authVal)
	}
	low.lowLevelNetwork.Auth = authValues

	return diags
}

func addStorage(low *lowLevelConfig, storage Storage, evalContext *hcl.EvalContext) hcl.Diagnostics {
	var diags hcl.Diagnostics

	attrs, moreDiags := storage.Config.JustAttributes()
	diags = append(diags, addSubjectToRelatedDiags(storage.DeclRange.Ptr(), moreDiags)...)
	if diags.HasErrors() {
		return diags
	}

	prefix := prefixForStorage(storage)

	vm := make(map[string]cty.Value)
	vm["type"] = cty.StringVal(storage.Type)
	for k, v := range attrs {
		vv, moreDiags := v.Expr.Value(evalContext)
		diags = append(diags, addSubjectToRelatedDiags(v.Range.Ptr(), moreDiags)...)
		vm[k] = vv
	}
	if diags.HasErrors() {
		return diags
	}
	obj := cty.ObjectVal(vm)

	resolved, moreDiags := resolveInlineStorages(obj, prefix, low.Prefixes)
	diags = append(diags, addSubjectToRelatedDiags(storage.DeclRange.Ptr(), moreDiags)...)
	if diags.HasErrors() {
		return diags
	}
	storageArgs, moreDiags := storageArgs(resolved)
	diags = append(diags, addSubjectToRelatedDiags(storage.DeclRange.Ptr(), moreDiags)...)
	if diags.HasErrors() {
		return diags
	}
	low.Prefixes[prefix] = lowLevelPrefix{
		Handler:     fmt.Sprintf("storage-%s", storage.Type),
		HandlerArgs: storageArgs,
	}
	return diags
}

// TODO: make stuff here more configurable, for now its ok to hardcode certain stuff
// to get a feeling for the configuration
func addPerkeepd(low *lowLevelConfig, server Server, evalContext *hcl.EvalContext) hcl.Diagnostics {
	var diags hcl.Diagnostics

	low.Prefixes[prefixRoot] = lowLevelPrefix{
		Handler: "root",
		HandlerArgs: map[string]interface{}{
			"blobRoot":     prefixBlobSourceAndMaybeAlsoIndex,
			"helpRoot":     prefixHelp,
			"jsonSignRoot": prefixJsonSign,
			"searchRoot":   prefixSearch,
			"statusRoot":   prefixStatus,
			"stealth":      false,
		},
	}

	low.Prefixes[prefixHelp] = lowLevelPrefix{
		Handler: "help",
	}

	low.Prefixes[prefixSetup] = lowLevelPrefix{
		Handler: "setup",
	}

	low.Prefixes[prefixSearch] = lowLevelPrefix{
		Handler: "search",
		HandlerArgs: map[string]interface{}{
			"index": prefixIndex,
			"owner": map[string]string{
				"identity":    server.Identity.ID,
				"secringFile": server.Identity.Keyring,
			},
			"slurpToMemory": true,
		},
	}

	low.Prefixes[prefixBlobSourceAndIndex] = lowLevelPrefix{
		Handler: "storage-replica",
		HandlerArgs: map[string]interface{}{
			"backends": []string{server.BlobSource, prefixIndex},
		},
	}

	low.Prefixes[prefixBlobSourceAndMaybeAlsoIndex] = lowLevelPrefix{
		Handler: "storage-cond",
		HandlerArgs: map[string]interface{}{
			"read": server.BlobSource,
			"write": map[string]interface{}{
				"if":   "isSchema",
				"then": prefixBlobSourceAndIndex,
				"else": server.BlobSource,
			},
		},
	}

	low.Prefixes[prefixJsonSign] = lowLevelPrefix{
		Handler: "jsonsign",
		HandlerArgs: map[string]interface{}{
			"keyId":         server.Identity.ID,
			"secretRing":    server.Identity.Keyring,
			"publicKeyDest": prefixBlobSourceAndIndex,
		},
	}

	low.Prefixes[prefixIndex] = lowLevelPrefix{
		Handler: "storage-index",
		HandlerArgs: map[string]interface{}{
			"blobSource": server.BlobSource,
			"keepGoing":  true,
			"reindex":    false,
			"storage": map[string]interface{}{
				"file": filepath.Join(server.DataDir, "index", "index.sqlite"),
				"type": "sqlite",
			},
		},
	}

	low.Prefixes[prefixSyncBlobsourceToIndex] = lowLevelPrefix{
		Handler: "sync",
		HandlerArgs: map[string]interface{}{
			"from": server.BlobSource,
			"to":   prefixIndex,
			"queue": map[string]interface{}{
				"file": filepath.Join(server.DataDir, "sync-bs-to-index", "queue.sqlite"),
				"type": "sqlite",
			},
		},
	}

	low.Prefixes[prefixUI] = lowLevelPrefix{
		Handler: "ui",
		HandlerArgs: map[string]interface{}{
			"cache": prefixCache,
			"scaledImage": map[string]interface{}{
				"file": filepath.Join(server.DataDir, "ui", "thumbmeta.sqlite"),
				"type": "sqlite",
			},
		},
	}

	low.Prefixes[prefixCache] = lowLevelPrefix{
		Handler: "storage-filesystem",
		HandlerArgs: map[string]interface{}{
			"path": filepath.Join(server.DataDir, "ui", "cache"),
		},
	}

	return diags
}

func prefixForStorage(storage Storage) string {
	if storage.Name != nil {
		return fmt.Sprintf("/storage-%s/", *storage.Name)
	}
	return fmt.Sprintf("/storage-%s/", storage.Type)
}

func addSubjectToRelatedDiags(subject *hcl.Range, moreDiags hcl.Diagnostics) hcl.Diagnostics {
	res := make(hcl.Diagnostics, len(moreDiags))
	for i := range moreDiags {
		res[i] = moreDiags[i]
		res[i].Subject = subject
	}
	return res
}
