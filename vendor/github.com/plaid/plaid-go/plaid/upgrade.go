package plaid

import (
	"bytes"
	"encoding/json"
)

// Upgrade (POST /upgrade) upgrades an access token to an additional product.
//
// See https://plaid.com/docs/api/#upgrade-user.
func (c *Client) Upgrade(accessToken, upgradeTo string,
	options *UpgradeOptions) (postRes *postResponse, mfaRes *mfaResponse, err error) {

	jsonText, err := json.Marshal(upgradeJson{
		ClientID:    c.clientID,
		Secret:      c.secret,
		AccessToken: accessToken,
		UpgradeTo:   upgradeTo,
		Options:     options,
	})
	if err != nil {
		return nil, nil, err
	}
	return c.postAndUnmarshal("/upgrade", bytes.NewReader(jsonText))
}

// UpgradeStepSendMethod (POST /upgrade/step) specifies a particular send method for MFA,
// e.g. {"mask":"xxx-xxx-5309"}.
//
// See https://plaid.com/docs/api/#upgrade-user.
func (c *Client) UpgradeStepSendMethod(accessToken, key, value string) (postRes *postResponse,
	mfaRes *mfaResponse, err error) {

	sendMethod := map[string]string{key: value}
	jsonText, err := json.Marshal(upgradeStepSendMethodJson{
		ClientID:    c.clientID,
		Secret:      c.secret,
		AccessToken: accessToken,
		Options:     upgradeStepOptions{sendMethod},
	})
	if err != nil {
		return nil, nil, err
	}
	return c.postAndUnmarshal("/upgrade/step", bytes.NewReader(jsonText))
}

// UpgradeStep (POST /upgrade/step) submits an MFA answer for a given access token.
//
// See https://plaid.com/docs/api/#mfa-authentication for upgrades to Connect.
// See https://plaid.com/docs/api/#mfa-auth for upgrades to Auth.
func (c *Client) UpgradeStep(accessToken, answer string) (postRes *postResponse,
	mfaRes *mfaResponse, err error) {

	jsonText, err := json.Marshal(upgradeStepJson{
		ClientID:    c.clientID,
		Secret:      c.secret,
		AccessToken: accessToken,
		MFA:         answer,
	})
	if err != nil {
		return nil, nil, err
	}
	return c.postAndUnmarshal("/upgrade/step", bytes.NewReader(jsonText))
}

// UpgradeOptions represents options associated with upgrading a user.
//
// See https://plaid.com/docs/api/#add-user for upgrades to Connect.
// See https://plaid.com/docs/api/#add-auth-user for upgrades to Auth.
type UpgradeOptions struct {
	Webhook string `json:"webhook,omitempty"`
}

type upgradeJson struct {
	ClientID    string          `json:"client_id"`
	Secret      string          `json:"secret"`
	AccessToken string          `json:"access_token"`
	UpgradeTo   string          `json:"upgrade_to"`
	Options     *UpgradeOptions `json:"options,omitempty"`
}

type upgradeStepOptions struct {
	SendMethod map[string]string `json:"send_method"`
}

type upgradeStepSendMethodJson struct {
	ClientID    string             `json:"client_id"`
	Secret      string             `json:"secret"`
	AccessToken string             `json:"access_token"`
	Options     upgradeStepOptions `json:"options"`
}

type upgradeStepJson struct {
	ClientID    string `json:"client_id"`
	Secret      string `json:"secret"`
	AccessToken string `json:"access_token"`
	MFA         string `json:"mfa"`
}
