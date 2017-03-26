package plaid

import (
	"bytes"
	"encoding/json"
)

// ExchangeToken (POST /exchange_token) exchanges a public token for an access token.
//
// See https://github.com/plaid/link
func (c *Client) ExchangeToken(publicToken string) (postRes *postResponse, err error) {
	jsonText, err := json.Marshal(exchangeJson{
		ClientID:    c.clientID,
		Secret:      c.secret,
		PublicToken: publicToken,
	})
	if err != nil {
		return nil, err
	}
	postRes, _, err = c.postAndUnmarshal("/exchange_token", bytes.NewReader(jsonText))
	return postRes, err
}

// ExchangeTokenAccount (POST /exchange_token) exchanges a public token and account id to receive a
// bank account token.
func (c *Client) ExchangeTokenAccount(publicToken string, accountId string) (postRes *postResponse, err error) {
	jsonText, err := json.Marshal(exchangeAccountJson{
		ClientID:    c.clientID,
		Secret:      c.secret,
		PublicToken: publicToken,
		AccountId:   accountId,
	})
	if err != nil {
		return nil, err
	}
	postRes, _, err = c.postAndUnmarshal("/exchange_token", bytes.NewReader(jsonText))
	return postRes, err
}

type exchangeJson struct {
	ClientID    string `json:"client_id"`
	Secret      string `json:"secret"`
	PublicToken string `json:"public_token"`
}

type exchangeAccountJson struct {
	ClientID    string `json:"client_id"`
	Secret      string `json:"secret"`
	PublicToken string `json:"public_token"`
	AccountId   string `json:"account_id"`
}
