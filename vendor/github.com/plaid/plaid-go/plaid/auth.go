package plaid

import (
	"bytes"
	"encoding/json"
)

// AuthAddUser (POST /auth) submits a set of user credentials to add an Auth user.
//
// See https://plaid.com/docs/api/#add-auth-user.
func (c *Client) AuthAddUser(username, password, pin, institutionType string,
	options *AuthOptions) (postRes *postResponse, mfaRes *mfaResponse, err error) {

	jsonText, err := json.Marshal(authJson{
		ClientID: c.clientID,
		Secret:   c.secret,
		Type:     institutionType,
		Username: username,
		Password: password,
		PIN:      pin,
		Options:  options,
	})
	if err != nil {
		return nil, nil, err
	}
	return c.postAndUnmarshal("/auth", bytes.NewReader(jsonText))
}

// AuthStepSendMethod (POST /auth/step) specifies a particular send method for MFA,
// e.g. `{"mask":"xxx-xxx-5309"}`.
//
// See https://plaid.com/docs/api/#auth-mfa.
func (c *Client) AuthStepSendMethod(accessToken, key, value string) (postRes *postResponse,
	mfaRes *mfaResponse, err error) {

	sendMethod := map[string]string{key: value}
	jsonText, err := json.Marshal(authStepSendMethodJson{
		ClientID:    c.clientID,
		Secret:      c.secret,
		AccessToken: accessToken,
		Options:     authStepOptions{sendMethod},
	})
	if err != nil {
		return nil, nil, err
	}
	return c.postAndUnmarshal("/auth/step", bytes.NewReader(jsonText))
}

// AuthStep (POST /auth/step) submits an MFA answer for a given access token.
//
// See https://plaid.com/docs/api/#auth-mfa.
func (c *Client) AuthStep(accessToken, answer string) (postRes *postResponse,
	mfaRes *mfaResponse, err error) {

	jsonText, err := json.Marshal(authStepJson{
		ClientID:    c.clientID,
		Secret:      c.secret,
		AccessToken: accessToken,
		MFA:         answer,
	})
	if err != nil {
		return nil, nil, err
	}
	return c.postAndUnmarshal("/auth/step", bytes.NewReader(jsonText))
}

// AuthGet (POST /auth/get) retrieves account data for a given access token.
//
// See https://plaid.com/docs/api/#get-auth-data.
func (c *Client) AuthGet(accessToken string) (postRes *postResponse, err error) {
	jsonText, err := json.Marshal(authGetJson{
		ClientID:    c.clientID,
		Secret:      c.secret,
		AccessToken: accessToken,
	})
	if err != nil {
		return nil, err
	}
	// /auth/get will never return an MFA response
	postRes, _, err = c.postAndUnmarshal("/auth/get", bytes.NewReader(jsonText))
	return postRes, err
}

// AuthUpdate (PATCH /auth) updates user credentials for a given access token.
//
// See https://plaid.com/docs/api/#update-auth-user.
func (c *Client) AuthUpdate(username, password, pin, accessToken string) (postRes *postResponse,
	mfaRes *mfaResponse, err error) {

	jsonText, err := json.Marshal(authUpdateJson{
		ClientID:    c.clientID,
		Secret:      c.secret,
		Username:    username,
		Password:    password,
		PIN:         pin,
		AccessToken: accessToken,
	})
	if err != nil {
		return nil, nil, err
	}
	return c.patchAndUnmarshal("/auth", bytes.NewReader(jsonText))
}

// AuthUpdateStep (PATCH /auth/step) updates user credentials and MFA for a given access token.
//
// See https://plaid.com/docs/api/#update-auth-user.
func (c *Client) AuthUpdateStep(username, password, pin, mfa, accessToken string) (postRes *postResponse,
	mfaRes *mfaResponse, err error) {

	jsonText, err := json.Marshal(authUpdateStepJson{
		ClientID:    c.clientID,
		Secret:      c.secret,
		Username:    username,
		Password:    password,
		PIN:         pin,
		MFA:         mfa,
		AccessToken: accessToken,
	})
	if err != nil {
		return nil, nil, err
	}
	return c.patchAndUnmarshal("/auth/step", bytes.NewReader(jsonText))
}

// AuthDelete (DELETE /auth) deletes data for a given access token.
//
// See https://plaid.com/docs/api/#delete-auth-user.
func (c *Client) AuthDelete(accessToken string) (deleteRes *deleteResponse, err error) {
	jsonText, err := json.Marshal(authDeleteJson{
		ClientID:    c.clientID,
		Secret:      c.secret,
		AccessToken: accessToken,
	})
	if err != nil {
		return nil, err
	}
	return c.deleteAndUnmarshal("/auth", bytes.NewReader(jsonText))
}

// AuthOptions represents options associated with adding an Auth user.
//
// See https://plaid.com/docs/api/#add-auth-user.
type AuthOptions struct {
	List bool `json:"list"`
}
type authJson struct {
	ClientID string `json:"client_id"`
	Secret   string `json:"secret"`
	Type     string `json:"type"`

	Username string       `json:"username"`
	Password string       `json:"password"`
	PIN      string       `json:"pin,omitempty"`
	Options  *AuthOptions `json:"options,omitempty"`
}

type authStepOptions struct {
	SendMethod map[string]string `json:"send_method"`
}

type authStepSendMethodJson struct {
	ClientID    string          `json:"client_id"`
	Secret      string          `json:"secret"`
	AccessToken string          `json:"access_token"`
	Options     authStepOptions `json:"options"`
}

type authStepJson struct {
	ClientID    string `json:"client_id"`
	Secret      string `json:"secret"`
	AccessToken string `json:"access_token"`

	MFA string `json:"mfa"`
}

type authGetJson struct {
	ClientID    string `json:"client_id"`
	Secret      string `json:"secret"`
	AccessToken string `json:"access_token"`
}

type authUpdateJson struct {
	ClientID string `json:"client_id"`
	Secret   string `json:"secret"`

	Username    string `json:"username"`
	Password    string `json:"password"`
	PIN         string `json:"pin,omitempty"`
	AccessToken string `json:"access_token"`
}

type authUpdateStepJson struct {
	ClientID string `json:"client_id"`
	Secret   string `json:"secret"`

	Username    string `json:"username"`
	Password    string `json:"password"`
	PIN         string `json:"pin,omitempty"`
	MFA         string `json:"mfa"`
	AccessToken string `json:"access_token"`
}

type authDeleteJson struct {
	ClientID    string `json:"client_id"`
	Secret      string `json:"secret"`
	AccessToken string `json:"access_token"`
}
