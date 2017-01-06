// Package plaid implements a Go client for the Plaid API (https://plaid.com/docs)
package plaid

import (
	"encoding/json"
	"errors"
	"io"
	"io/ioutil"
	"net/http"
)

// NewClient instantiates a Client associated with a client id, secret and environment.
// See https://plaid.com/docs/api/#gaining-access.
func NewClient(clientID, secret string, environment environmentURL) *Client {
	return &Client{clientID, secret, environment, &http.Client{}}
}

// Same as above but with additional parameter to pass http.Client. This is required
// if you want to run the code on Google AppEngine which prohibits use of http.DefaultClient
func NewCustomClient(clientID, secret string, environment environmentURL, httpClient *http.Client) *Client {
	return &Client{clientID, secret, environment, httpClient}
}

// Note: Client is only exported for method documentation purposes.
// Instances should only be created through the 'NewClient' function.
//
// See https://github.com/golang/go/issues/7823.
type Client struct {
	clientID    string
	secret      string
	environment environmentURL
	httpClient  *http.Client
}

type environmentURL string

var Tartan environmentURL = "https://tartan.plaid.com"
var Production environmentURL = "https://api.plaid.com"

type Account struct {
	ID      string `json:"_id"`
	ItemID  string `json:"_item"`
	UserID  string `json:"_user"`
	Balance struct {
		Available float64 `json:"available"`
		Current   float64 `json:"current"`
	} `json:"balance"`
	Meta struct {
		Number string `json:"number"`
		Name   string `json:"name"`
	} `json:"meta"`
	Numbers struct {
		Account     string `json:"account"`
		Routing     string `json:"routing"`
		WireRouting string `json:"wireRouting"`
	} `json:"numbers"`
	Type            string `json:"type"`
	InstitutionType string `json:"institution_type"`
}

type Transaction struct {
	ID        string `json:"_id"`
	AccountID string `json:"_account"`

	Amount float64 `json:"amount"`
	Date   string  `json:"date"`
	Name   string  `json:"name"`
	Meta   struct {
		AccountOwner string `json:"account_owner"`

		Location struct {
			Address string `json:"address"`
			City    string `json:"city"`

			Coordinates struct {
				Lat float64 `json:"lat"`
				Lon float64 `json:"lon"`
			} `json:"coordinates"`

			State string `json:"state"`
			Zip   string `json:"zip"`
		} `json:"location"`
	} `json:"meta"`

	Pending bool `json:"pending"`

	Type struct {
		Primary string `json:"primary"`
	} `json:"type"`

	Category   []string `json:"category"`
	CategoryID string   `json:"category_id"`

	Score struct {
		Location struct {
			Address float64 `json:"address"`
			City    float64 `json:"city"`
			State   float64 `json:"state"`
			Zip     float64 `json:"zip"`
		}
		Name float64 `json:"name"`
	} `json:"score"`
}

type mfaIntermediate struct {
	AccessToken string      `json:"access_token"`
	MFA         interface{} `json:"mfa"`
	Type        string      `json:"type"`
}
type mfaDevice struct {
	Message string
}
type mfaList struct {
	Mask string
	Type string
}
type mfaQuestion struct {
	Question string
}
type mfaSelection struct {
	Answers  []string
	Question string
}

// 'mfa' contains the union of all possible mfa types
// Users should switch on the 'Type' field
type mfaResponse struct {
	AccessToken string
	Type        string

	Device     mfaDevice
	List       []mfaList
	Questions  []mfaQuestion
	Selections []mfaSelection
}

type postResponse struct {
	// Normal response fields
	AccessToken      string        `json:"access_token"`
	AccountId        string        `json:"account_id"`
	Accounts         []Account     `json:"accounts"`
	BankAccountToken string        `json:"stripe_bank_account_token"`
	MFA              string        `json:"mfa"`
	Transactions     []Transaction `json:"transactions"`
}

type deleteResponse struct {
	Message string `json:"message"`
}

// getAndUnmarshal is not a method because no client authentication is required
func getAndUnmarshal(environment environmentURL, endpoint string, structure interface{}) error {
	res, err := http.Get(string(environment) + endpoint)
	if err != nil {
		return err
	}
	raw, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return err
	}
	res.Body.Close()

	// Successful response
	if res.StatusCode == 200 {
		if err = json.Unmarshal(raw, structure); err != nil {
			return err
		}
		return nil
	}
	// Attempt to unmarshal into Plaid error format
	var plaidErr plaidError
	if err = json.Unmarshal(raw, &plaidErr); err != nil {
		return err
	}
	plaidErr.StatusCode = res.StatusCode
	return plaidErr
}

func (c *Client) postAndUnmarshal(endpoint string,
	body io.Reader) (*postResponse, *mfaResponse, error) {
	// Read response body
	req, err := http.NewRequest("POST", string(c.environment)+endpoint, body)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("User-Agent", "plaid-go")
	res, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	raw, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return nil, nil, err
	}
	res.Body.Close()

	return unmarshalPostMFA(res, raw)
}

func (c *Client) patchAndUnmarshal(endpoint string,
	body io.Reader) (*postResponse, *mfaResponse, error) {

	req, err := http.NewRequest("PATCH", string(c.environment)+endpoint, body)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("User-Agent", "plaid-go")
	res, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	raw, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return nil, nil, err
	}
	res.Body.Close()

	return unmarshalPostMFA(res, raw)
}

func (c *Client) deleteAndUnmarshal(endpoint string,
	body io.Reader) (*deleteResponse, error) {

	req, err := http.NewRequest("DELETE", string(c.environment)+endpoint, body)
	if err != nil {
		return nil, err
	}
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("User-Agent", "plaid-go")
	res, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	raw, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	res.Body.Close()

	// Successful response
	var deleteRes deleteResponse
	if res.StatusCode == 200 {
		if err = json.Unmarshal(raw, &deleteRes); err != nil {
			return nil, err
		}
		return &deleteRes, nil
	}
	// Attempt to unmarshal into Plaid error format
	var plaidErr plaidError
	if err = json.Unmarshal(raw, &plaidErr); err != nil {
		return nil, err
	}
	plaidErr.StatusCode = res.StatusCode
	return nil, plaidErr
}

// Unmarshals response into postResponse, mfaResponse, or plaidError
func unmarshalPostMFA(res *http.Response, body []byte) (*postResponse, *mfaResponse, error) {
	// Different marshaling cases
	var mfaInter mfaIntermediate
	var postRes postResponse
	var err error
	switch {
	// Successful response
	case res.StatusCode == 200:
		if err = json.Unmarshal(body, &postRes); err != nil {
			return nil, nil, err
		}
		return &postRes, nil, nil

	// MFA case
	case res.StatusCode == 201:
		if err = json.Unmarshal(body, &mfaInter); err != nil {
			return nil, nil, err
		}
		mfaRes := mfaResponse{Type: mfaInter.Type, AccessToken: mfaInter.AccessToken}
		switch mfaInter.Type {
		case "device":
			temp, ok := mfaInter.MFA.(interface{})
			if !ok {
				return nil, nil, errors.New("Could not decode device mfa")
			}
			deviceStruct, ok := temp.(map[string]interface{})
			if !ok {
				return nil, nil, errors.New("Could not decode device mfa")
			}
			deviceText, ok := deviceStruct["message"].(string)
			if !ok {
				return nil, nil, errors.New("Could not decode device mfa")
			}
			mfaRes.Device.Message = deviceText

		case "list":
			temp, ok := mfaInter.MFA.([]interface{})
			if !ok {
				return nil, nil, errors.New("Could not decode list mfa")
			}
			for _, v := range temp {
				listArray, ok := v.(map[string]interface{})
				if !ok {
					return nil, nil, errors.New("Could not decode list mfa")
				}
				maskText, ok := listArray["mask"].(string)
				if !ok {
					return nil, nil, errors.New("Could not decode list mfa")
				}
				typeText, ok := listArray["type"].(string)
				if !ok {
					return nil, nil, errors.New("Could not decode list mfa")
				}
				mfaRes.List = append(mfaRes.List, mfaList{Mask: maskText, Type: typeText})
			}

		case "questions":
			questions, ok := mfaInter.MFA.([]interface{})
			if !ok {
				return nil, nil, errors.New("Could not decode questions mfa")
			}
			for _, v := range questions {
				q, ok := v.(map[string]interface{})
				if !ok {
					return nil, nil, errors.New("Could not decode questions mfa")
				}
				questionText, ok := q["question"].(string)
				if !ok {
					return nil, nil, errors.New("Could not decode questions mfa question")
				}
				mfaRes.Questions = append(mfaRes.Questions, mfaQuestion{Question: questionText})
			}

		case "selections":
			selections, ok := mfaInter.MFA.([]interface{})
			if !ok {
				return nil, nil, errors.New("Could not decode selections mfa")
			}
			for _, v := range selections {
				s, ok := v.(map[string]interface{})
				if !ok {
					return nil, nil, errors.New("Could not decode selections mfa")
				}
				tempAnswers, ok := s["answers"].([]interface{})
				if !ok {
					return nil, nil, errors.New("Could not decode selections answers")
				}
				answers := make([]string, len(tempAnswers))
				for i, a := range tempAnswers {
					answers[i], ok = a.(string)
				}
				if !ok {
					return nil, nil, errors.New("Could not decode selections answers")
				}
				question, ok := s["question"].(string)
				if !ok {
					return nil, nil, errors.New("Could not decode selections questions")
				}
				mfaRes.Selections = append(mfaRes.Selections, mfaSelection{Answers: answers, Question: question})
			}
		}
		return nil, &mfaRes, nil

	// Error case, attempt to unmarshal into Plaid error format
	case res.StatusCode >= 400:
		var plaidErr plaidError
		if err = json.Unmarshal(body, &plaidErr); err != nil {
			return nil, nil, err
		}
		plaidErr.StatusCode = res.StatusCode
		return nil, nil, plaidErr
	}
	return nil, nil, errors.New("Unknown Plaid Error - Status:" + string(res.StatusCode))
}
