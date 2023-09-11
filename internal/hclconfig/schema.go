package hclconfig

import (
	"fmt"
	"reflect"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/gocty"
)

const (
	// arbitary limitation, since we need to declare inline storages beforehand
	// and it is not clear how to declare unknown indexes in cty paths we add
	// this many paths there to resolve inline handlers. When computing the args for
	// storages of type 'replica' we need to verify that we have at most this many
	maxReplicas = 100
)

func storageArgs(obj cty.Value) (map[string]interface{}, hcl.Diagnostics) {
	var diags hcl.Diagnostics

	storageType, moreDiags := typeFromObject(obj)
	diags = append(diags, moreDiags...)
	if diags.HasErrors() {
		return nil, diags
	}

	switch storageType {
	case "azure":
		return storageArgsAzure(obj)
	case "b2":
		return storageArgsB2(obj)
	case "blobpacked":
		return storageArgsBlobpacked(obj)
	case "cond":
		return storageArgsCond(obj)
	case "diskpacked":
		return storageArgsDiskpacked(obj)
	case "encrypt":
		return storageArgsEncrypt(obj)
	case "googlecloudstorage":
		return storageArgsGoogleCloudStorage(obj)
	case "googledrive":
		return storageArgsGoogleDrive(obj)
	case "localdisk":
		return storageArgsLocaldisk(obj)
	case "memory":
		return storageArgsMemory(obj)
	case "mongo":
		return storageArgsMongo(obj)
	case "namespace":
		return storageArgsNamespace(obj)
	case "overlay":
		return storageArgsOverlay(obj)
	case "proxycache":
		return storageArgsProxycache(obj)
	case "remote":
		return storageArgsRemote(obj)
	case "replica":
		return storageArgsReplica(obj)
	default:
		return nil, diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Unknown storage type",
			Detail:   fmt.Sprintf("The storage type '%s' is not known", storageType),
		})
	}
}

func indexArgs(obj cty.Value) (map[string]interface{}, hcl.Diagnostics) {
	var diags hcl.Diagnostics

	indexType, moreDiags := typeFromObject(obj)
	diags = append(diags, moreDiags...)
	if diags.HasErrors() {
		return nil, diags
	}

	switch indexType {
	case "kv":
		return indexArgsKVFile(obj)
	case "leveldb":
		return indexArgsLevelDB(obj)
	case "mongodb":
		return indexArgsMongoDB(obj)
	case "mysql":
		return indexArgsMySQL(obj)
	case "postgres":
		return indexArgsPostgres(obj)
	case "sqlite":
		return indexArgsSQLite(obj)
	case "memory":
		return indexArgsMemory(obj)
	default:
		return nil, diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Unknown index type",
			Detail:   fmt.Sprintf("The index type '%s' is not known", indexType),
		})
	}
}

// like indexArgs but with an additional "type" key in the resulting map
// since all storages have this index as an inline object with this type
func indexArgsWithType(obj cty.Value) (map[string]interface{}, hcl.Diagnostics) {
	var diags hcl.Diagnostics

	res, moreDiags := indexArgs(obj)
	diags = append(diags, moreDiags...)
	if diags.HasErrors() {
		return nil, diags
	}
	typ, moreDiags := typeFromObject(obj)
	diags = append(diags, moreDiags...)
	if diags.HasErrors() {
		return nil, diags
	}
	res["type"] = typ
	return res, diags
}

// Storage schemas

func storageArgsAzure(obj cty.Value) (map[string]interface{}, hcl.Diagnostics) {
	var diags hcl.Diagnostics

	raw := struct {
		Type             string  `cty:"type"`
		AzureAccount     string  `cty:"azure_account"`
		AzureAccessKey   string  `cty:"azure_access_key"`
		Container        string  `cty:"container"`
		HostName         *string `cty:"hostname"`
		CacheSize        *uint64 `cty:"cache_size"`
		SkipStartupCheck *bool   `cty:"skip_startup_check"`
	}{}
	if err := gocty.FromCtyValue(obj, &raw); err != nil {
		return nil, diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Bad azure storage",
			Detail:   fmt.Sprintf("Unable to decode azure storage: %s", err),
		})
	}

	res := map[string]interface{}{
		"azure_account":    raw.AzureAccount,
		"azure_access_key": raw.AzureAccessKey,
		"container":        raw.Container,
		"hostname":         orDefault(raw.HostName, ""),
		"cacheSize":        orDefault(raw.CacheSize, 32<<20),
		"skipStartupCheck": orDefault(raw.SkipStartupCheck, false),
	}

	return res, diags
}

func storageArgsB2(obj cty.Value) (map[string]interface{}, hcl.Diagnostics) {
	var diags hcl.Diagnostics

	raw := struct {
		Type string `cty:"type"`
		Auth struct {
			AccountId      string `cty:"account_id"`
			ApplicationKey string `cty:"application_key"`
		} `cty:"auth"`
		Bucket    string  `cty:"bucket"`
		CacheSize *uint64 `cty:"cache_size"`
	}{}
	if err := gocty.FromCtyValue(obj, &raw); err != nil {
		return nil, diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Bad b2 storage",
			Detail:   fmt.Sprintf("Unable to decode b2 storage: %s", err),
		})
	}

	res := map[string]interface{}{
		"auth": map[string]interface{}{
			"account_id":      raw.Auth.AccountId,
			"application_key": raw.Auth.ApplicationKey,
		},
		"bucket":    raw.Bucket,
		"cacheSize": orDefault(raw.CacheSize, 32<<20),
	}

	return res, diags
}

func storageArgsBlobpacked(obj cty.Value) (map[string]interface{}, hcl.Diagnostics) {
	var diags hcl.Diagnostics

	raw := struct {
		Type       string    `cty:"type"`
		KeepGoing  *bool     `cty:"keep_going"`
		SmallBlobs string    `cty:"small_blobs"`
		LargeBlobs string    `cty:"large_blobs"`
		MetaIndex  cty.Value `cty:"meta_index"`
	}{}
	if err := gocty.FromCtyValue(obj, &raw); err != nil {
		return nil, diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Bad blobpacked storage",
			Detail:   fmt.Sprintf("Unable to decode blobpacked storage: %s", err),
		})
	}

	indexArgs, moreDiags := indexArgsWithType(raw.MetaIndex)
	diags = append(diags, moreDiags...)
	if diags.HasErrors() {
		return nil, diags
	}

	res := map[string]interface{}{
		"keepGoing":  orDefault(raw.KeepGoing, false),
		"smallBlobs": raw.SmallBlobs,
		"largeBlobs": raw.LargeBlobs,
		"metaIndex":  indexArgs,
	}

	return res, diags
}

func storageArgsCond(obj cty.Value) (map[string]interface{}, hcl.Diagnostics) {
	var diags hcl.Diagnostics

	// defer decoding 'write' it may either be an object or a string
	raw := struct {
		Type   string    `cty:"type"`
		Read   string    `cty:"read"`
		Remove *string   `cty:"remove"`
		Write  cty.Value `cty:"write"`
	}{}

	if err := gocty.FromCtyValue(obj, &raw); err != nil {
		return nil, diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Bad cond storage",
			Detail:   fmt.Sprintf("Unable to decode cond storage: %s", err),
		})
	}

	res := map[string]interface{}{
		"read":   raw.Read,
		"remove": orDefault(raw.Remove, ""),
	}

	// write could either be a string or a resolved object at this point
	if raw.Write.Type().IsObjectType() {
		rawWrite := struct {
			If   string `cty:"if"`
			Then string `cty:"then"`
			Else string `cty:"else"`
		}{}
		if err := gocty.FromCtyValue(raw.Write, &rawWrite); err != nil {
			return nil, diags.Append(&hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  "Bad cond storage",
				Detail:   fmt.Sprintf("Unable to decode 'write' field of cond storage: %s", err),
			})
		}
		res["write"] = map[string]interface{}{
			"if":   rawWrite.If,
			"then": rawWrite.Then,
			"else": rawWrite.Else,
		}
	} else if raw.Write.Type() == cty.String {
		res["write"] = raw.Write.AsString()
	} else {
		return nil, diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Bad cond storage",
			Detail:   fmt.Sprintf("Bad 'write' parameter in cond storage: expected reference or object"),
		})
	}

	return res, diags
}

func storageArgsDiskpacked(obj cty.Value) (map[string]interface{}, hcl.Diagnostics) {
	var diags hcl.Diagnostics

	raw := struct {
		Type        string     `cty:"type"`
		Path        string     `cty:"path"`
		MaxFileSize *int       `cty:"max_file_size"`
		MetaIndex   *cty.Value `cty:"meta_index"`
	}{}
	if err := gocty.FromCtyValue(obj, &raw); err != nil {
		return nil, diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Bad diskpacked storage",
			Detail:   fmt.Sprintf("Unable to decode diskpacked storage: %s", err),
		})
	}

	res := map[string]interface{}{
		"path":        raw.Path,
		"maxFileSize": orDefault(raw.MaxFileSize, 0),
	}

	if raw.MetaIndex == nil {
		return res, diags
	}

	index, moreDiags := indexArgsWithType(*raw.MetaIndex)
	diags = append(diags, moreDiags...)
	if diags.HasErrors() {
		return nil, diags
	}

	res["metaIndex"] = index

	return res, diags
}

func storageArgsEncrypt(obj cty.Value) (map[string]interface{}, hcl.Diagnostics) {
	var diags hcl.Diagnostics

	raw := struct {
		Type       string    `cty:"type"`
		Agreement  string    `cty:"I_AGREE"`
		Blobs      string    `cty:"blobs"`
		Meta       string    `cty:"meta"`
		MetaIndex  cty.Value `cty:"meta_index"`
		Passphrase *string   `cty:"passphrase"`
		KeyFile    *string   `cty:"key_file"`
	}{}
	if err := gocty.FromCtyValue(obj, &raw); err != nil {
		return nil, diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Bad encrypt storage",
			Detail:   fmt.Sprintf("Unable to decode encrypt storage: %s", err),
		})
	}

	index, moreDiags := indexArgsWithType(raw.MetaIndex)
	diags = append(diags, moreDiags...)
	if diags.HasErrors() {
		return nil, diags
	}

	res := map[string]interface{}{
		"I_AGREE":    raw.Agreement,
		"blobs":      raw.Blobs,
		"meta":       raw.Meta,
		"metaIndex":  index,
		"keyFile":    orDefault(raw.KeyFile, ""),
		"passphrase": orDefault(raw.Passphrase, ""),
	}

	return res, diags
}

func storageArgsGoogleCloudStorage(obj cty.Value) (map[string]interface{}, hcl.Diagnostics) {
	var diags hcl.Diagnostics

	raw := struct {
		Type string `cty:"type"`
		Auth struct {
			ClientId     string  `cty:"client_id"`
			ClientSecret *string `cty:"client_secret"`
			RefreshToken *string `cty:"refresh_token"`
		} `cty:"auth"`
		Bucket    string  `cty:"bucket"`
		CacheSize *uint64 `cty:"cache_size"`
	}{}
	if err := gocty.FromCtyValue(obj, &raw); err != nil {
		return nil, diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Bad google cloud storage",
			Detail:   fmt.Sprintf("Unable to decode google cloud storage: %s", err),
		})
	}

	res := map[string]interface{}{
		"auth": map[string]interface{}{
			"client_id":     raw.Auth.ClientId,
			"client_secret": orDefault(raw.Auth.ClientSecret, ""),
			"refresh_token": orDefault(raw.Auth.RefreshToken, ""),
		},
		"bucket":    raw.Bucket,
		"cacheSize": orDefault(raw.CacheSize, 32<<20),
	}

	return res, diags
}

func storageArgsGoogleDrive(obj cty.Value) (map[string]interface{}, hcl.Diagnostics) {
	var diags hcl.Diagnostics

	raw := struct {
		Type string `cty:"type"`
		Auth struct {
			ClientId     string  `cty:"client_id"`
			ClientSecret *string `cty:"client_secret"`
			RefreshToken *string `cty:"refresh_token"`
		} `cty:"auth"`
		ParentId string `cty:"parent_id"`
	}{}
	if err := gocty.FromCtyValue(obj, &raw); err != nil {
		return nil, diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Bad google drive storage",
			Detail:   fmt.Sprintf("Unable to decode google drive storage: %s", err),
		})
	}

	res := map[string]interface{}{
		"auth": map[string]interface{}{
			"client_id":     raw.Auth.ClientId,
			"client_secret": orDefault(raw.Auth.ClientSecret, ""),
			"refresh_token": orDefault(raw.Auth.RefreshToken, ""),
		},
		"parent_id": raw.ParentId,
	}

	return res, diags
}

func storageArgsLocaldisk(obj cty.Value) (map[string]interface{}, hcl.Diagnostics) {
	var diags hcl.Diagnostics

	raw := struct {
		Type string `cty:"type"`
		Path string `cty:"path"`
	}{}
	if err := gocty.FromCtyValue(obj, &raw); err != nil {
		return nil, diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Bad localdisk storage",
			Detail:   fmt.Sprintf("Unable to decode localdisk storage: %s", err),
		})
	}

	res := map[string]interface{}{
		"path": raw.Path,
	}

	return res, diags
}

func storageArgsMemory(obj cty.Value) (map[string]interface{}, hcl.Diagnostics) {
	var diags hcl.Diagnostics

	raw := struct {
		Type string `cty:"type"`
	}{}
	if err := gocty.FromCtyValue(obj, &raw); err != nil {
		return nil, diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Bad memory storage",
			Detail:   fmt.Sprintf("Unable to decode memory storage: %s", err),
		})
	}

	res := map[string]interface{}{}

	return res, diags
}

func storageArgsMongo(obj cty.Value) (map[string]interface{}, hcl.Diagnostics) {
	var diags hcl.Diagnostics

	raw := struct {
		Type       string  `cty:"type"`
		Host       *string `cty:"host"`
		Database   string  `cty:"database"`
		Collection *string `cty:"collection"`
		User       *string `cty:"user"`
		Password   *string `cty:"password"`
	}{}
	if err := gocty.FromCtyValue(obj, &raw); err != nil {
		return nil, diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Bad mongo storage",
			Detail:   fmt.Sprintf("Unable to decode mongo storage: %s", err),
		})
	}

	res := map[string]interface{}{
		"host":       orDefault(raw.Host, "localhost"),
		"database":   raw.Database,
		"collection": orDefault(raw.Collection, "blobs"),
		"user":       orDefault(raw.User, ""),
		"password":   orDefault(raw.Password, ""),
	}

	return res, diags
}

func storageArgsNamespace(obj cty.Value) (map[string]interface{}, hcl.Diagnostics) {
	var diags hcl.Diagnostics

	raw := struct {
		Type      string    `cty:"type"`
		Storage   string    `cty:"storage"`
		Inventory cty.Value `cty:"inventory"`
	}{}
	if err := gocty.FromCtyValue(obj, &raw); err != nil {
		return nil, diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Bad namespace storage",
			Detail:   fmt.Sprintf("Unable to decode namespace storage: %s", err),
		})
	}

	index, moreDiags := indexArgsWithType(raw.Inventory)
	diags = append(diags, moreDiags...)
	if diags.HasErrors() {
		return nil, diags
	}

	res := map[string]interface{}{
		"storage":   raw.Storage,
		"inventory": index,
	}

	return res, diags
}

func storageArgsOverlay(obj cty.Value) (map[string]interface{}, hcl.Diagnostics) {
	var diags hcl.Diagnostics

	raw := struct {
		Type    string     `cty:"type"`
		Upper   string     `cty:"upper"`
		Lower   string     `cty:"lower"`
		Deleted *cty.Value `cty:"deleted"`
	}{}
	if err := gocty.FromCtyValue(obj, &raw); err != nil {
		return nil, diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Bad overlay storage",
			Detail:   fmt.Sprintf("Unable to decode overlay storage: %s", err),
		})
	}

	res := map[string]interface{}{
		"upper": raw.Upper,
		"lower": raw.Lower,
	}

	if raw.Deleted == nil {
		return res, diags
	}

	index, moreDiags := indexArgsWithType(*raw.Deleted)
	diags = append(diags, moreDiags...)
	if diags.HasErrors() {
		return nil, diags
	}
	res["deleted"] = index

	return res, diags
}

func storageArgsProxycache(obj cty.Value) (map[string]interface{}, hcl.Diagnostics) {
	var diags hcl.Diagnostics

	raw := struct {
		Type          string `cty:"type"`
		Origin        string `cty:"origin"`
		Cache         string `cty:"cache"`
		MaxCacheBytes *int   `cty:"max_cache_bytes"`
	}{}
	if err := gocty.FromCtyValue(obj, &raw); err != nil {
		return nil, diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Bad proxycache storage",
			Detail:   fmt.Sprintf("Unable to decode proxycache storage: %s", err),
		})
	}

	res := map[string]interface{}{
		"origin":        raw.Origin,
		"cache":         raw.Cache,
		"maxCacheBytes": orDefault(raw.MaxCacheBytes, 512<<20),
	}

	return res, diags
}

func storageArgsRemote(obj cty.Value) (map[string]interface{}, hcl.Diagnostics) {
	var diags hcl.Diagnostics

	raw := struct {
		Type             string  `cty:"type"`
		URL              string  `cty:"url"`
		Auth             string  `cty:"auth"`
		SkipStartupCheck *bool   `cty:"skip_startup_check"`
		TrustedCert      *string `cty:"trusted_cert"`
	}{}
	if err := gocty.FromCtyValue(obj, &raw); err != nil {
		return nil, diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Bad remote storage",
			Detail:   fmt.Sprintf("Unable to decode remote storage: %s", err),
		})
	}

	res := map[string]interface{}{
		"url":              raw.URL,
		"auth":             raw.Auth,
		"skipStartupCheck": orDefault(raw.SkipStartupCheck, false),
		"trustedCert":      orDefault(raw.TrustedCert, ""),
	}

	return res, diags
}

func storageArgsReplica(obj cty.Value) (map[string]interface{}, hcl.Diagnostics) {
	var diags hcl.Diagnostics

	// backends and read_backends get parsed as tuple for some reason, also validate that we were
	// able to flatten all replicas by checking that we have less than maxReplicas
	val, err := cty.Transform(obj, func(p cty.Path, v cty.Value) (cty.Value, error) {
		if !v.Type().IsTupleType() {
			return v, nil
		}
		if p.Equals(cty.GetAttrPath("read_backends")) || p.Equals(cty.GetAttrPath("backends")) {
			if numReplicas := len(v.AsValueSlice()); numReplicas > maxReplicas {
				return v, p.NewError(fmt.Errorf("Too many replicas: %d > %d", numReplicas, maxReplicas))
			}
		}
		return cty.ListVal(v.AsValueSlice()), nil
	})
	if err != nil {
		return nil, diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Bad replica storage",
			Detail:   fmt.Sprintf("Invalid replica configuration: %s", err),
		})
	}

	raw := struct {
		Type                string   `cty:"type"`
		Backends            []string `cty:"backends"`
		ReadBackends        []string `cty:"read_backends"`
		MinWritesForSuccess *int     `cty:"min_writes_for_success"`
	}{}
	if err := gocty.FromCtyValue(val, &raw); err != nil {
		return nil, diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Bad replica storage",
			Detail:   fmt.Sprintf("Unable to decode replica storage: %s", err),
		})
	}

	res := map[string]interface{}{
		"backends":            raw.Backends,
		"readBackends":        raw.ReadBackends,
		"minWritesForSuccess": orDefault(raw.MinWritesForSuccess, len(raw.Backends)),
	}

	return res, diags
}

// Index handlers

func indexArgsKVFile(obj cty.Value) (map[string]interface{}, hcl.Diagnostics) {
	var diags hcl.Diagnostics

	raw := struct {
		Type string `cty:"type"`
		File string `cty:"file"`
	}{}
	if err := gocty.FromCtyValue(obj, &raw); err != nil {
		return nil, diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Bad kv index",
			Detail:   fmt.Sprintf("Unable to decode kv index: %s", err),
		})
	}

	res := map[string]interface{}{
		"file": raw.File,
	}

	return res, diags
}

func indexArgsLevelDB(obj cty.Value) (map[string]interface{}, hcl.Diagnostics) {
	var diags hcl.Diagnostics

	raw := struct {
		Type string `cty:"type"`
		File string `cty:"file"`
	}{}
	if err := gocty.FromCtyValue(obj, &raw); err != nil {
		return nil, diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Bad leveldb index",
			Detail:   fmt.Sprintf("Unable to decode leveldb index: %s", err),
		})
	}

	res := map[string]interface{}{
		"file": raw.File,
	}

	return res, diags
}

func indexArgsMongoDB(obj cty.Value) (map[string]interface{}, hcl.Diagnostics) {
	var diags hcl.Diagnostics

	raw := struct {
		Type     string  `cty:"type"`
		Database string  `cty:"database"`
		Host     *string `cty:"host"`
		User     *string `cty:"user"`
		Password *string `cty:"password"`
	}{}
	if err := gocty.FromCtyValue(obj, &raw); err != nil {
		return nil, diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Bad mongodb index",
			Detail:   fmt.Sprintf("Unable to decode mongodb index: %s", err),
		})
	}

	res := map[string]interface{}{
		"database": raw.Database,
		"host":     orDefault(raw.Host, "localhost"),
		"user":     orDefault(raw.User, ""),
		"password": orDefault(raw.Password, ""),
	}

	return res, diags
}

func indexArgsMySQL(obj cty.Value) (map[string]interface{}, hcl.Diagnostics) {
	var diags hcl.Diagnostics

	raw := struct {
		Type     string  `cty:"type"`
		User     string  `cty:"user"`
		Database string  `cty:"database"`
		Host     *string `cty:"host"`
		Password *string `cty:"password"`
	}{}
	if err := gocty.FromCtyValue(obj, &raw); err != nil {
		return nil, diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Bad mysql index",
			Detail:   fmt.Sprintf("Unable to decode mysql index: %s", err),
		})
	}

	res := map[string]interface{}{
		"user":     raw.User,
		"database": raw.Database,
		"host":     orDefault(raw.Host, ""),
		"password": orDefault(raw.Password, ""),
	}

	return res, diags
}

func indexArgsPostgres(obj cty.Value) (map[string]interface{}, hcl.Diagnostics) {
	var diags hcl.Diagnostics

	raw := struct {
		Type     string  `cty:"type"`
		User     string  `cty:"user"`
		Database string  `cty:"database"`
		Host     *string `cty:"host"`
		Password *string `cty:"password"`
		SSLMode  *string `cty:"sslmode"`
	}{}
	if err := gocty.FromCtyValue(obj, &raw); err != nil {
		return nil, diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Bad postgres index",
			Detail:   fmt.Sprintf("Unable to decode postgres index: %s", err),
		})
	}

	res := map[string]interface{}{
		"user":     raw.User,
		"database": raw.Database,
		"host":     orDefault(raw.Host, "localhost"),
		"password": orDefault(raw.Password, ""),
		"sslmode":  orDefault(raw.SSLMode, "require"),
	}

	return res, diags
}

func indexArgsSQLite(obj cty.Value) (map[string]interface{}, hcl.Diagnostics) {
	var diags hcl.Diagnostics

	raw := struct {
		Type string `cty:"type"`
		File string `cty:"file"`
	}{}
	if err := gocty.FromCtyValue(obj, &raw); err != nil {
		return nil, diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Bad sqlite index",
			Detail:   fmt.Sprintf("Unable to decode sqlite index: %s", err),
		})
	}

	res := map[string]interface{}{
		"file": raw.File,
	}

	return res, diags
}

func indexArgsMemory(obj cty.Value) (map[string]interface{}, hcl.Diagnostics) {
	var diags hcl.Diagnostics

	raw := struct {
		Type string `cty:"type"`
	}{}
	if err := gocty.FromCtyValue(obj, &raw); err != nil {
		return nil, diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Bad memory index",
			Detail:   fmt.Sprintf("Unable to decode memory index: %s", err),
		})
	}

	res := map[string]interface{}{}

	return res, nil
}

// Utilities

func typeFromObject(obj cty.Value) (string, hcl.Diagnostics) {
	var diags hcl.Diagnostics

	if !obj.Type().IsObjectType() {
		return "", diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Value must be object",
		})
	}
	if !obj.Type().HasAttribute("type") {
		return "", diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Value must have a 'type' attribute",
		})
	}
	typeVal := obj.GetAttr("type")
	if typeVal.Type() != cty.String {
		return "", diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Value attribute 'type' must be a string",
		})
	}
	return typeVal.AsString(), diags
}

/*
given a more recent compiler we would prefer to do:

func orDefault[T any](x *T, d T) T {
	if x == nil {
		return d
	}
	return *x
}
*/

// TODO just a quick hack for now until we get go1.18
func orDefault(x interface{}, d interface{}) interface{} {
	v := reflect.ValueOf(x)

	if v.IsNil() {
		return d
	}
	return v.Elem().Interface()
}

// Inline storages

func inlineStoragePaths(obj cty.Value) (cty.PathSet, hcl.Diagnostics) {
	var diags hcl.Diagnostics

	storageType, moreDiags := typeFromObject(obj)
	diags = append(diags, moreDiags...)
	if diags.HasErrors() {
		return cty.NewPathSet(), diags
	}

	switch storageType {
	case "blobpacked":
		return cty.NewPathSet(
			cty.GetAttrPath("small_blobs"),
			cty.GetAttrPath("large_blobs"),
		), diags
	case "cond":
		return cty.NewPathSet(
			cty.GetAttrPath("read"),
			cty.GetAttrPath("remove"),
			cty.GetAttrPath("write").GetAttr("then"),
			cty.GetAttrPath("write").GetAttr("else"),
		), diags
	case "encrypt":
		return cty.NewPathSet(
			cty.GetAttrPath("blobs"),
			cty.GetAttrPath("meta"),
		), diags
	case "namespace":
		return cty.NewPathSet(
			cty.GetAttrPath("storage"),
		), diags
	case "overlay":
		return cty.NewPathSet(
			cty.GetAttrPath("upper"),
			cty.GetAttrPath("lower"),
			cty.GetAttrPath("deleted"),
		), diags
	case "proxycache":
		return cty.NewPathSet(
			cty.GetAttrPath("origin"),
			cty.GetAttrPath("cache"),
			cty.GetAttrPath("deleted"),
		), diags
	case "replica":
		res := cty.NewPathSet()
		for i := 0; i < maxReplicas; i++ {
			res.Add(cty.GetAttrPath("backends").IndexInt(i))
			res.Add(cty.GetAttrPath("read_backends").IndexInt(i))
		}
		return res, diags
	default:
		return cty.NewPathSet(), diags
	}
}
