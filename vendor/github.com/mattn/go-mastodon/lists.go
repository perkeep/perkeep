package mastodon

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// List is metadata for a list of users.
type List struct {
	ID    ID     `json:"id"`
	Title string `json:"title"`
}

// GetLists returns all the lists on the current account.
func (c *Client) GetLists(ctx context.Context) ([]*List, error) {
	var lists []*List
	err := c.doAPI(ctx, http.MethodGet, "/api/v1/lists", nil, &lists, nil)
	if err != nil {
		return nil, err
	}
	return lists, nil
}

// GetAccountLists returns the lists containing a given account.
func (c *Client) GetAccountLists(ctx context.Context, id ID) ([]*List, error) {
	var lists []*List
	err := c.doAPI(ctx, http.MethodGet, fmt.Sprintf("/api/v1/accounts/%s/lists", url.PathEscape(string(id))), nil, &lists, nil)
	if err != nil {
		return nil, err
	}
	return lists, nil
}

// GetListAccounts returns the accounts in a given list.
func (c *Client) GetListAccounts(ctx context.Context, id ID) ([]*Account, error) {
	var accounts []*Account
	err := c.doAPI(ctx, http.MethodGet, fmt.Sprintf("/api/v1/lists/%s/accounts", url.PathEscape(string(id))), url.Values{"limit": {"0"}}, &accounts, nil)
	if err != nil {
		return nil, err
	}
	return accounts, nil
}

// GetList retrieves a list by ID.
func (c *Client) GetList(ctx context.Context, id ID) (*List, error) {
	var list List
	err := c.doAPI(ctx, http.MethodGet, fmt.Sprintf("/api/v1/lists/%s", url.PathEscape(string(id))), nil, &list, nil)
	if err != nil {
		return nil, err
	}
	return &list, nil
}

// CreateList creates a new list with a given title.
func (c *Client) CreateList(ctx context.Context, title string) (*List, error) {
	params := url.Values{}
	params.Set("title", title)

	var list List
	err := c.doAPI(ctx, http.MethodPost, "/api/v1/lists", params, &list, nil)
	if err != nil {
		return nil, err
	}
	return &list, nil
}

// RenameList assigns a new title to a list.
func (c *Client) RenameList(ctx context.Context, id ID, title string) (*List, error) {
	params := url.Values{}
	params.Set("title", title)

	var list List
	err := c.doAPI(ctx, http.MethodPut, fmt.Sprintf("/api/v1/lists/%s", url.PathEscape(string(id))), params, &list, nil)
	if err != nil {
		return nil, err
	}
	return &list, nil
}

// DeleteList removes a list.
func (c *Client) DeleteList(ctx context.Context, id ID) error {
	return c.doAPI(ctx, http.MethodDelete, fmt.Sprintf("/api/v1/lists/%s", url.PathEscape(string(id))), nil, nil, nil)
}

// AddToList adds accounts to a list.
//
// Only accounts already followed by the user can be added to a list.
func (c *Client) AddToList(ctx context.Context, list ID, accounts ...ID) error {
	params := url.Values{}
	for _, acct := range accounts {
		params.Add("account_ids", string(acct))
	}

	return c.doAPI(ctx, http.MethodPost, fmt.Sprintf("/api/v1/lists/%s/accounts", url.PathEscape(string(list))), params, nil, nil)
}

// RemoveFromList removes accounts from a list.
func (c *Client) RemoveFromList(ctx context.Context, list ID, accounts ...ID) error {
	params := url.Values{}
	for _, acct := range accounts {
		params.Add("account_ids", string(acct))
	}

	return c.doAPI(ctx, http.MethodDelete, fmt.Sprintf("/api/v1/lists/%s/accounts", url.PathEscape(string(list))), params, nil, nil)
}
