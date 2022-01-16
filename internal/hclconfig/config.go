package hclconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/zclconf/go-cty/cty"
)

type Config struct {
	Network Network
	Server  Server
	Storage []Storage
	Syncs   []Sync

	EvalContext *hcl.EvalContext
}

func ParseHCLBodyToLowLevelConfig(body hcl.Body) (map[string]interface{}, error) {
	var diags hcl.Diagnostics

	evalContext, moreDiags := buildEvalContext(body)
	diags = append(diags, moreDiags...)
	if diags.HasErrors() {
		return nil, errors.New(diags.Error())
	}

	config, moreDiags := decodeConfig(body, evalContext)
	diags = append(diags, moreDiags...)
	if diags.HasErrors() {
		return nil, errors.New(diags.Error())
	}

	lowLevelConfig, moreDiags := compileToLowLevelConfig(config)
	diags = append(diags, moreDiags...)
	if diags.HasErrors() {
		return nil, errors.New(diags.Error())
	}

	// feels a little dirty, but for now map[string]interface{} is expected in the configuration layer
	lowLevelBytes, err := json.Marshal(lowLevelConfig)
	if err != nil {
		return nil, fmt.Errorf("unable to marshal low level config: %w", err)
	}

	var m map[string]interface{}
	if err = json.Unmarshal(lowLevelBytes, &m); err != nil {
		return nil, fmt.Errorf("unable to unmarshal low level config bytes: %w", err)
	}
	return m, nil
}

func buildEvalContext(body hcl.Body) (*hcl.EvalContext, hcl.Diagnostics) {
	// We have all information to build the eval context in the first pass.
	// Take care to first add the enviroment to be able to use it to evaluate the other variables
	var (
		diags   hcl.Diagnostics
		evalCtx = &hcl.EvalContext{Variables: make(map[string]cty.Value)}
	)

	buildEvalContextAddEnvironment(evalCtx)
	diags = append(diags, buildEvalContextAddVariables(body, evalCtx)...)
	diags = append(diags, buildEvalContextAddStorage(body, evalCtx)...)

	return evalCtx, diags
}

func buildEvalContextAddEnvironment(evalCtx *hcl.EvalContext) {
	const evalCtxKeyEnvironment = "env"

	aux := make(map[string]cty.Value)
	for _, envVal := range os.Environ() {
		envSplit := strings.SplitN(envVal, "=", 2)
		aux[envSplit[0]] = cty.StringVal(envSplit[1])
	}
	evalCtx.Variables[evalCtxKeyEnvironment] = cty.ObjectVal(aux)
}

func buildEvalContextAddVariables(body hcl.Body, evalCtx *hcl.EvalContext) hcl.Diagnostics {
	const evalCtxKeyVariable = "var"

	var (
		diags hcl.Diagnostics
		aux   = make(map[string]cty.Value)
	)

	content, _, moreDiags := body.PartialContent(&hcl.BodySchema{Blocks: []hcl.BlockHeaderSchema{
		{Type: "variables"},
	}})
	diags = append(diags, moreDiags...)

	for _, block := range content.Blocks {
		varAttrs, moreDiags := block.Body.JustAttributes()
		diags = append(diags, moreDiags...)

		for k, v := range varAttrs {
			vv, moreDiags := v.Expr.Value(evalCtx)
			diags = append(diags, moreDiags...)
			aux[k] = vv
		}
		evalCtx.Variables[evalCtxKeyVariable] = cty.ObjectVal(aux)
	}

	return diags
}

func buildEvalContextAddStorage(body hcl.Body, evalCtx *hcl.EvalContext) hcl.Diagnostics {
	const evalCtxKeyStorage = "storage"
	var (
		diags hcl.Diagnostics
		aux   = make(map[string]cty.Value)
	)

	content, _, moreDiags := body.PartialContent(&hcl.BodySchema{Blocks: []hcl.BlockHeaderSchema{
		{Type: "storage", LabelNames: []string{"type"}},
	}})
	diags = append(diags, moreDiags...)

	for _, block := range content.Blocks {
		storage, moreDiags := decodeStorageBlock(block, evalCtx)
		diags = append(diags, moreDiags...)
		if diags.HasErrors() {
			continue
		}

		var key string
		if storage.Name != nil {
			key = *storage.Name
		} else {
			key = storage.Type
		}
		aux[key] = cty.StringVal(fmt.Sprintf("/storage-%s/", key))
	}
	evalCtx.Variables[evalCtxKeyStorage] = cty.ObjectVal(aux)

	return diags
}

const (
	typeNetwork   = "network"
	typeServer    = "server"
	typeStorage   = "storage"
	typeSync      = "sync"
	typeVariables = "variables"
)

func decodeConfig(body hcl.Body, evalContext *hcl.EvalContext) (Config, hcl.Diagnostics) {
	var (
		diags      hcl.Diagnostics
		rootSchema = &hcl.BodySchema{
			Blocks: []hcl.BlockHeaderSchema{
				{Type: typeVariables},
				{Type: typeNetwork},
				{Type: typeServer},
				{Type: typeStorage, LabelNames: []string{"type"}},
				{Type: typeSync},
			},
		}
	)

	content, moreDiags := body.Content(rootSchema)
	diags = append(diags, moreDiags...)

	evalContext, moreDiags = buildEvalContext(body)
	diags = append(diags, moreDiags...)

	config := Config{
		Storage:     make([]Storage, 0),
		EvalContext: evalContext,
	}

	// to produce diagnostics for blocks that should not appear twice
	seenBlockTypes := make(map[string]*hcl.Block)
	for _, block := range content.Blocks {
		if prev, ok := seenBlockTypes[block.Type]; ok && isUniqueBlockType(block.Type) {
			diags = diags.Append(&hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  fmt.Sprintf("Duplicate '%s' block", block.Type),
				Detail:   fmt.Sprintf("The '%s' settings were already configured at %s.", block.Type, prev.DefRange),
				Subject:  &block.TypeRange,
			})
			continue
		}
		seenBlockTypes[block.Type] = block

		switch block.Type {
		case typeNetwork:
			decodedNetwork, moreDiags := decodeNetworkBlock(block, evalContext)
			diags = append(diags, moreDiags...)
			config.Network = decodedNetwork
		case typeServer:
			server, moreDiags := decodeServerBlock(block, evalContext)
			diags = append(diags, moreDiags...)
			config.Server = server
		case typeStorage:
			// TODO: error diag on duplicate storage block name
			storage, moreDiags := decodeStorageBlock(block, evalContext)
			diags = append(diags, moreDiags...)
			config.Storage = append(config.Storage, storage)
		case typeSync:
			sync, moreDiags := decodeSyncBlock(block, evalContext)
			diags = append(diags, moreDiags...)
			config.Syncs = append(config.Syncs, sync)
		case typeVariables:
			// used for eval context
			continue
		default:
			return config, diags.Append(&hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  fmt.Sprintf("Unknown block type '%s'", block.Type),
				Detail:   fmt.Sprintf("The block type '%s' is not known.", block.Type),
				Subject:  &block.TypeRange,
			})
		}
	}
	// TODO: check that seenBlockTypes contains all necessary block types
	return config, diags
}

func isUniqueBlockType(blockType string) bool {
	nonUniqueBlocks := map[string]struct{}{
		typeStorage: {},
		typeSync:    {},
	}
	_, ok := nonUniqueBlocks[blockType]
	return !ok
}

type Network struct {
	Listen     string
	CamliNetIP string
	BaseURL    string
	HTTPS      bool
	HTTPSCert  string
	HTTPSKey   string
	Auths      []Auth

	DeclRange hcl.Range
}

func decodeNetworkBlock(block *hcl.Block, evalCtx *hcl.EvalContext) (Network, hcl.Diagnostics) {
	type networkRaw struct {
		Listen     string   `hcl:"listen,attr"`
		CamliNetIP string   `hcl:"camlinet_ip,optional"`
		BaseURL    string   `hcl:"base_url,optional"`
		HTTPS      bool     `hcl:"https,optional"`
		HTTPSCert  string   `hcl:"https_cert,optional"`
		HTTPSKey   string   `hcl:"https_key,optional"`
		Remain     hcl.Body `hcl:",remain"`
	}

	var diags hcl.Diagnostics

	network := Network{
		Auths: make([]Auth, 0),

		DeclRange: block.DefRange,
	}

	var raw networkRaw
	diags = append(diags, gohcl.DecodeBody(block.Body, evalCtx, &raw)...)
	if diags.HasErrors() {
		return network, diags
	}

	network.Listen = raw.Listen
	network.CamliNetIP = raw.CamliNetIP
	network.BaseURL = raw.BaseURL
	network.HTTPS = raw.HTTPS
	network.HTTPSCert = raw.HTTPSCert
	network.HTTPSKey = raw.HTTPSKey

	authBlockHeaderSchema := hcl.BlockHeaderSchema{Type: "auth", LabelNames: []string{"type"}}
	remainingContent, moreDiags := raw.Remain.Content(&hcl.BodySchema{Blocks: []hcl.BlockHeaderSchema{authBlockHeaderSchema}})
	diags = append(diags, moreDiags...)
	if diags.HasErrors() {
		return network, diags
	}

	for _, block := range remainingContent.Blocks {
		auth, moreDiags := decodeAuth(block, evalCtx)
		diags = append(diags, moreDiags...)
		if !diags.HasErrors() {
			network.Auths = append(network.Auths, auth)
		}
	}

	return network, diags
}

type Server struct {
	Identity   Identity
	DataDir    string
	BlobSource string

	DeclRange hcl.Range
}

type Identity struct {
	ID      string
	Keyring string
}

func decodeServerBlock(block *hcl.Block, evalCtx *hcl.EvalContext) (Server, hcl.Diagnostics) {
	type serverRaw struct {
		DataDir    *string        `hcl:"data_dir"`
		BlobSource string         `hcl:"blob_source"`
		Identity   hcl.Expression `hcl:"identity"`
	}
	type identityRaw struct {
		ID      string `cty:"id"`
		Keyring string `cty:"keyring"`
	}

	var diags hcl.Diagnostics

	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return Server{}, diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  fmt.Sprintf("Unable to determine cache directory"),
			Detail:   fmt.Sprintf("Error determining cache directory: %s", err),
			Subject:  &block.TypeRange,
		})
	}

	server := Server{
		DataDir:   filepath.Join(cacheDir, "perkeepd"),
		DeclRange: block.DefRange,
	}

	var sRaw serverRaw
	diags = append(diags, gohcl.DecodeBody(block.Body, evalCtx, &sRaw)...)
	if diags.HasErrors() {
		return server, diags
	}

	var idRaw identityRaw
	diags = append(diags, gohcl.DecodeExpression(sRaw.Identity, evalCtx, &idRaw)...)
	if diags.HasErrors() {
		return server, diags
	}

	if sRaw.DataDir != nil {
		server.DataDir = *sRaw.DataDir
	}
	server.BlobSource = sRaw.BlobSource
	server.Identity.ID = idRaw.ID
	server.Identity.Keyring = idRaw.Keyring

	return server, diags
}

type Storage struct {
	Type   string
	Name   *string
	Config hcl.Body

	TypeRange hcl.Range
	DeclRange hcl.Range
}

func decodeStorageBlock(block *hcl.Block, evalCtx *hcl.EvalContext) (Storage, hcl.Diagnostics) {
	type storageRaw struct {
		Name   string   `hcl:"name,optional"`
		Remain hcl.Body `hcl:",remain"`
	}

	var diags hcl.Diagnostics

	storage := Storage{
		Type:   block.Labels[0],
		Config: block.Body,

		TypeRange: block.LabelRanges[0],
		DeclRange: block.DefRange,
	}

	var raw storageRaw
	diags = append(diags, gohcl.DecodeBody(block.Body, evalCtx, &raw)...)
	if diags.HasErrors() {
		return storage, diags
	}

	storage.Config = raw.Remain

	if raw.Name != "" {
		storage.Name = &raw.Name
	}

	return storage, diags
}

type Sync struct {
	From           string
	To             string
	VerifyInterval time.Duration

	DeclRange hcl.Range
}

func decodeSyncBlock(block *hcl.Block, ctx *hcl.EvalContext) (Sync, hcl.Diagnostics) {
	type syncRaw struct {
		From           string         `hcl:"from"`
		To             string         `hcl:"to"`
		VerifyInterval hcl.Expression `hcl:"verify_interval"`
	}

	sync := Sync{
		DeclRange: block.DefRange,
	}

	var raw syncRaw
	diags := gohcl.DecodeBody(block.Body, ctx, &raw)
	if diags.HasErrors() {
		return sync, diags
	}

	sync.From = raw.From
	sync.To = raw.To

	if raw.VerifyInterval != nil {
		var durStr string
		moreDiags := gohcl.DecodeExpression(raw.VerifyInterval, ctx, &durStr)
		diags = append(diags, moreDiags...)
		if !moreDiags.HasErrors() {
			dur, err := time.ParseDuration(durStr)
			if err != nil {
				diags = append(diags, &hcl.Diagnostic{
					Severity: hcl.DiagError,
					Summary:  "Invalid \"verify_interval\" argument",
					Detail:   fmt.Sprintf("The \"verify_interval\" value is not a valid duration string: %s.", err),
					Subject:  raw.VerifyInterval.Range().Ptr(),
				})
			}
			sync.VerifyInterval = dur
		}
	}

	return sync, diags
}

type Auth struct {
	Type string   `hcl:"type,label"`
	Body hcl.Body `hcl:",remain"`

	TypeRange hcl.Range // to report if "Type" is not a valid auth type
	DeclRange hcl.Range
}

func decodeAuth(block *hcl.Block, evalCtx *hcl.EvalContext) (Auth, hcl.Diagnostics) {
	return Auth{
		Type: block.Labels[0],
		Body: block.Body,

		TypeRange: block.LabelRanges[0],
		DeclRange: block.DefRange,
	}, nil
}

// renderAuth renders an Auth struct to the expected perkeep auth string
//
// TODO: add remaining auth types
func renderAuth(auth Auth, evalCtx *hcl.EvalContext) (string, hcl.Diagnostics) {
	const (
		authTypeNone      = "none"
		authTypeLocalhost = "localhost"
		authTypeBasic     = "basic"
	)

	var diags hcl.Diagnostics

	switch auth.Type {
	case authTypeLocalhost:
		diags = append(diags, gohcl.DecodeBody(auth.Body, evalCtx, &struct{}{})...)
		if diags.HasErrors() {
			return "", diags
		}
		return "localhost", diags
	case authTypeNone:
		diags = append(diags, gohcl.DecodeBody(auth.Body, evalCtx, &struct{}{})...)
		if diags.HasErrors() {
			return "", diags
		}
		return "none", diags
	case authTypeBasic:
		type raw struct {
			Username string `hcl:"username"`
			Password string `hcl:"password"`
		}
		var aux raw
		diags = append(diags, gohcl.DecodeBody(auth.Body, evalCtx, &aux)...)
		if diags.HasErrors() {
			return "", diags
		}
		return fmt.Sprintf("basic:%s:%s", aux.Username, aux.Password), diags
	default:
		return "", diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Invalid 'auth' block type.",
			Detail:   fmt.Sprintf("'%s' is an unknown type for 'auth' blocks.", auth.Type),
			Subject:  &auth.TypeRange,
		})
	}
}
