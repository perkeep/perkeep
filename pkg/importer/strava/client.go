/*
Copyright 2026 The Perkeep Authors

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

package strava

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	apiBaseURL = "https://www.strava.com/api/v3"
	tokenURL   = "https://www.strava.com/oauth/token"

	maxAPIResponseBytes   = 32 << 20
	maxTokenResponseBytes = 1 << 20
	streamKeys            = "time,latlng,altitude,distance,heartrate,cadence,watts,temp,moving,velocity_smooth"
)

type apiClient struct {
	httpClient  *http.Client
	accessToken string
}

type activity struct {
	ID                 int64     `json:"id"`
	Name               string    `json:"name"`
	Description        string    `json:"description"`
	Type               string    `json:"type"`
	SportType          string    `json:"sport_type"`
	Timezone           string    `json:"timezone"`
	ExternalID         string    `json:"external_id"`
	GearID             string    `json:"gear_id"`
	StartDate          time.Time `json:"start_date"`
	Distance           float64   `json:"distance"`
	TotalElevationGain float64   `json:"total_elevation_gain"`
	MovingTime         int64     `json:"moving_time"`
	ElapsedTime        int64     `json:"elapsed_time"`
	StartLatlng        []float64 `json:"start_latlng"`
	Trainer            bool      `json:"trainer"`
	Commute            bool      `json:"commute"`
	Manual             bool      `json:"manual"`
	Private            bool      `json:"private"`
	AverageHeartrate   *float64  `json:"average_heartrate"`
	MaxHeartrate       *float64  `json:"max_heartrate"`
	Calories           *float64  `json:"calories"`
}

type athlete struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	Firstname string `json:"firstname"`
	Lastname  string `json:"lastname"`
}

type tokenResponse struct {
	AccessToken  string  `json:"access_token"`
	RefreshToken string  `json:"refresh_token"`
	ExpiresAt    int64   `json:"expires_at"`
	Athlete      athlete `json:"athlete"`
}

type streams map[string]stream

type stream struct {
	Data         json.RawMessage `json:"data"`
	SeriesType   string          `json:"series_type"`
	OriginalSize int             `json:"original_size"`
	Resolution   string          `json:"resolution"`
}

func (c *apiClient) listActivities(ctx context.Context, page int) ([]activity, error) {
	if page < 1 {
		return nil, errors.New("strava: page must be at least 1")
	}
	query := url.Values{"page": {strconv.Itoa(page)}, "per_page": {"100"}}
	raw, _, err := c.get(ctx, "/athlete/activities?"+query.Encode(), false)
	if err != nil {
		return nil, err
	}
	var activities []activity
	if json.Unmarshal(raw, &activities) != nil || activities == nil {
		return nil, errors.New("strava: invalid activity list response")
	}
	for _, a := range activities {
		if a.ID <= 0 {
			return nil, errors.New("strava: invalid activity ID in list response")
		}
	}
	return activities, nil
}

func (c *apiClient) getActivity(ctx context.Context, id int64) (*activity, json.RawMessage, error) {
	if id <= 0 {
		return nil, nil, errors.New("strava: activity ID must be positive")
	}
	raw, _, err := c.get(ctx, "/activities/"+strconv.FormatInt(id, 10), false)
	if err != nil {
		return nil, nil, err
	}
	var a activity
	if json.Unmarshal(raw, &a) != nil || a.ID != id {
		return nil, nil, errors.New("strava: invalid activity response")
	}
	return &a, raw, nil
}

func (c *apiClient) getStreams(ctx context.Context, id int64) (streams, json.RawMessage, error) {
	if id <= 0 {
		return nil, nil, errors.New("strava: activity ID must be positive")
	}
	query := url.Values{"keys": {streamKeys}, "key_by_type": {"true"}}
	raw, missing, err := c.get(ctx, "/activities/"+strconv.FormatInt(id, 10)+"/streams?"+query.Encode(), true)
	if err != nil {
		return nil, nil, err
	}
	if missing {
		return streams{}, nil, nil
	}
	var s streams
	if json.Unmarshal(raw, &s) != nil || s == nil {
		return nil, nil, errors.New("strava: invalid streams response")
	}
	return s, raw, nil
}

func (c *apiClient) get(ctx context.Context, path string, allowNotFound bool) (json.RawMessage, bool, error) {
	if strings.TrimSpace(c.accessToken) == "" {
		return nil, false, errors.New("strava: missing access token")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBaseURL+path, nil)
	if err != nil {
		return nil, false, errors.New("strava: cannot construct API request")
	}
	req.Header.Set("Authorization", "Bearer "+c.accessToken)
	req.Header.Set("Accept", "application/json")
	return readResponse(c.httpClient, req, maxAPIResponseBytes, allowNotFound)
}

// exchangeToken handles both authorization-code exchanges and refreshes. The
// caller supplies the appropriate grant_type and credentials in form.
func exchangeToken(ctx context.Context, client *http.Client, form url.Values) (*tokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, errors.New("strava: cannot construct token request")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	raw, _, err := readResponse(client, req, maxTokenResponseBytes, false)
	if err != nil {
		return nil, err
	}
	var token tokenResponse
	if json.Unmarshal(raw, &token) != nil {
		return nil, errors.New("strava: invalid token response")
	}
	if strings.TrimSpace(token.AccessToken) == "" || strings.TrimSpace(token.RefreshToken) == "" || token.ExpiresAt <= time.Now().Unix() {
		return nil, errors.New("strava: token response has missing tokens or invalid expiry")
	}
	return &token, nil
}

// responseError deliberately omits server bodies, status text, headers, and
// transport error text: any of these can contain OAuth credentials. Unwrap
// retains errors.Is support, notably for cancellation and deadline expiry.
type responseError struct {
	operation string
	cause     error
}

func (e *responseError) Error() string { return "strava: " + e.operation + " failed" }
func (e *responseError) Unwrap() error { return e.cause }

func readResponse(client *http.Client, req *http.Request, limit int64, allowNotFound bool) (json.RawMessage, bool, error) {
	if client == nil {
		client = http.DefaultClient
	}
	// Do not forward bearer tokens or POST credentials through redirects. Copy
	// the client rather than mutating the importer host's shared client.
	noRedirect := *client
	if noRedirect.Timeout == 0 {
		noRedirect.Timeout = 60 * time.Second
	}
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := noRedirect.Do(req)
	if err != nil {
		return nil, false, &responseError{"HTTP request", err}
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound && allowNotFound {
		return nil, true, nil
	}
	if res.StatusCode == http.StatusTooManyRequests {
		return nil, false, errors.New("strava: rate limit exceeded (HTTP 429)")
	}
	if res.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("strava: unexpected HTTP status %d", res.StatusCode)
	}
	if res.ContentLength > limit {
		return nil, false, errors.New("strava: response exceeds size limit")
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, false, &responseError{"reading response", err}
	}
	if int64(len(raw)) > limit {
		return nil, false, errors.New("strava: response exceeds size limit")
	}
	return raw, false, nil
}
