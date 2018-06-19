package mastodon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"path"
	"strings"
)

// AppConfig is a setting for registering applications.
type AppConfig struct {
	http.Client
	Server     string
	ClientName string

	// Where the user should be redirected after authorization (for no redirect, use urn:ietf:wg:oauth:2.0:oob)
	RedirectURIs string

	// This can be a space-separated list of the following items: "read", "write" and "follow".
	Scopes string

	// Optional.
	Website string
}

// Application is mastodon application.
type Application struct {
	ID           ID     `json:"id"`
	RedirectURI  string `json:"redirect_uri"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

// RegisterApp returns the mastodon application.
func RegisterApp(ctx context.Context, appConfig *AppConfig) (*Application, error) {
	params := url.Values{}
	params.Set("client_name", appConfig.ClientName)
	if appConfig.RedirectURIs == "" {
		params.Set("redirect_uris", "urn:ietf:wg:oauth:2.0:oob")
	} else {
		params.Set("redirect_uris", appConfig.RedirectURIs)
	}
	params.Set("scopes", appConfig.Scopes)
	params.Set("website", appConfig.Website)

	u, err := url.Parse(appConfig.Server)
	if err != nil {
		return nil, err
	}
	u.Path = path.Join(u.Path, "/api/v1/apps")

	req, err := http.NewRequest(http.MethodPost, u.String(), strings.NewReader(params.Encode()))
	if err != nil {
		return nil, err
	}
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := appConfig.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, parseAPIError("bad request", resp)
	}

	var app Application
	err = json.NewDecoder(resp.Body).Decode(&app)
	if err != nil {
		return nil, err
	}

	return &app, nil
}
