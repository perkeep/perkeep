package mastodon

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Status is struct to hold status.
type Status struct {
	ID                 ID           `json:"id"`
	URI                string       `json:"uri"`
	URL                string       `json:"url"`
	Account            Account      `json:"account"`
	InReplyToID        interface{}  `json:"in_reply_to_id"`
	InReplyToAccountID interface{}  `json:"in_reply_to_account_id"`
	Reblog             *Status      `json:"reblog"`
	Content            string       `json:"content"`
	CreatedAt          time.Time    `json:"created_at"`
	Emojis             []Emoji      `json:"emojis"`
	RepliesCount       int64        `json:"replies_count"`
	ReblogsCount       int64        `json:"reblogs_count"`
	FavouritesCount    int64        `json:"favourites_count"`
	Reblogged          interface{}  `json:"reblogged"`
	Favourited         interface{}  `json:"favourited"`
	Muted              interface{}  `json:"muted"`
	Sensitive          bool         `json:"sensitive"`
	SpoilerText        string       `json:"spoiler_text"`
	Visibility         string       `json:"visibility"`
	MediaAttachments   []Attachment `json:"media_attachments"`
	Mentions           []Mention    `json:"mentions"`
	Tags               []Tag        `json:"tags"`
	Card               *Card        `json:"card"`
	Application        Application  `json:"application"`
	Language           string       `json:"language"`
	Pinned             interface{}  `json:"pinned"`
}

// Context hold information for mastodon context.
type Context struct {
	Ancestors   []*Status `json:"ancestors"`
	Descendants []*Status `json:"descendants"`
}

// Card hold information for mastodon card.
type Card struct {
	URL          string `json:"url"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	Image        string `json:"image"`
	Type         string `json:"type"`
	AuthorName   string `json:"author_name"`
	AuthorURL    string `json:"author_url"`
	ProviderName string `json:"provider_name"`
	ProviderURL  string `json:"provider_url"`
	HTML         string `json:"html"`
	Width        int64  `json:"width"`
	Height       int64  `json:"height"`
}

// GetFavourites return the favorite list of the current user.
func (c *Client) GetFavourites(ctx context.Context, pg *Pagination) ([]*Status, error) {
	var statuses []*Status
	err := c.doAPI(ctx, http.MethodGet, "/api/v1/favourites", nil, &statuses, pg)
	if err != nil {
		return nil, err
	}
	return statuses, nil
}

// GetStatus return status specified by id.
func (c *Client) GetStatus(ctx context.Context, id ID) (*Status, error) {
	var status Status
	err := c.doAPI(ctx, http.MethodGet, fmt.Sprintf("/api/v1/statuses/%s", id), nil, &status, nil)
	if err != nil {
		return nil, err
	}
	return &status, nil
}

// GetStatusContext return status specified by id.
func (c *Client) GetStatusContext(ctx context.Context, id ID) (*Context, error) {
	var context Context
	err := c.doAPI(ctx, http.MethodGet, fmt.Sprintf("/api/v1/statuses/%s/context", id), nil, &context, nil)
	if err != nil {
		return nil, err
	}
	return &context, nil
}

// GetStatusCard return status specified by id.
func (c *Client) GetStatusCard(ctx context.Context, id ID) (*Card, error) {
	var card Card
	err := c.doAPI(ctx, http.MethodGet, fmt.Sprintf("/api/v1/statuses/%s/card", id), nil, &card, nil)
	if err != nil {
		return nil, err
	}
	return &card, nil
}

// GetRebloggedBy returns the account list of the user who reblogged the toot of id.
func (c *Client) GetRebloggedBy(ctx context.Context, id ID, pg *Pagination) ([]*Account, error) {
	var accounts []*Account
	err := c.doAPI(ctx, http.MethodGet, fmt.Sprintf("/api/v1/statuses/%s/reblogged_by", id), nil, &accounts, pg)
	if err != nil {
		return nil, err
	}
	return accounts, nil
}

// GetFavouritedBy returns the account list of the user who liked the toot of id.
func (c *Client) GetFavouritedBy(ctx context.Context, id ID, pg *Pagination) ([]*Account, error) {
	var accounts []*Account
	err := c.doAPI(ctx, http.MethodGet, fmt.Sprintf("/api/v1/statuses/%s/favourited_by", id), nil, &accounts, pg)
	if err != nil {
		return nil, err
	}
	return accounts, nil
}

// Reblog is reblog the toot of id and return status of reblog.
func (c *Client) Reblog(ctx context.Context, id ID) (*Status, error) {
	var status Status
	err := c.doAPI(ctx, http.MethodPost, fmt.Sprintf("/api/v1/statuses/%s/reblog", id), nil, &status, nil)
	if err != nil {
		return nil, err
	}
	return &status, nil
}

// Unreblog is unreblog the toot of id and return status of the original toot.
func (c *Client) Unreblog(ctx context.Context, id ID) (*Status, error) {
	var status Status
	err := c.doAPI(ctx, http.MethodPost, fmt.Sprintf("/api/v1/statuses/%s/unreblog", id), nil, &status, nil)
	if err != nil {
		return nil, err
	}
	return &status, nil
}

// Favourite is favourite the toot of id and return status of the favourite toot.
func (c *Client) Favourite(ctx context.Context, id ID) (*Status, error) {
	var status Status
	err := c.doAPI(ctx, http.MethodPost, fmt.Sprintf("/api/v1/statuses/%s/favourite", id), nil, &status, nil)
	if err != nil {
		return nil, err
	}
	return &status, nil
}

// Unfavourite is unfavourite the toot of id and return status of the unfavourite toot.
func (c *Client) Unfavourite(ctx context.Context, id ID) (*Status, error) {
	var status Status
	err := c.doAPI(ctx, http.MethodPost, fmt.Sprintf("/api/v1/statuses/%s/unfavourite", id), nil, &status, nil)
	if err != nil {
		return nil, err
	}
	return &status, nil
}

// GetTimelineHome return statuses from home timeline.
func (c *Client) GetTimelineHome(ctx context.Context, pg *Pagination) ([]*Status, error) {
	var statuses []*Status
	err := c.doAPI(ctx, http.MethodGet, "/api/v1/timelines/home", nil, &statuses, pg)
	if err != nil {
		return nil, err
	}
	return statuses, nil
}

// GetTimelinePublic return statuses from public timeline.
func (c *Client) GetTimelinePublic(ctx context.Context, isLocal bool, pg *Pagination) ([]*Status, error) {
	params := url.Values{}
	if isLocal {
		params.Set("local", "t")
	}

	var statuses []*Status
	err := c.doAPI(ctx, http.MethodGet, "/api/v1/timelines/public", params, &statuses, pg)
	if err != nil {
		return nil, err
	}
	return statuses, nil
}

// GetTimelineHashtag return statuses from tagged timeline.
func (c *Client) GetTimelineHashtag(ctx context.Context, tag string, isLocal bool, pg *Pagination) ([]*Status, error) {
	params := url.Values{}
	if isLocal {
		params.Set("local", "t")
	}

	var statuses []*Status
	err := c.doAPI(ctx, http.MethodGet, fmt.Sprintf("/api/v1/timelines/tag/%s", url.PathEscape(tag)), params, &statuses, pg)
	if err != nil {
		return nil, err
	}
	return statuses, nil
}

// GetTimelineList return statuses from a list timeline.
func (c *Client) GetTimelineList(ctx context.Context, id ID, pg *Pagination) ([]*Status, error) {
	var statuses []*Status
	err := c.doAPI(ctx, http.MethodGet, fmt.Sprintf("/api/v1/timelines/list/%s", url.PathEscape(string(id))), nil, &statuses, pg)
	if err != nil {
		return nil, err
	}
	return statuses, nil
}

// GetTimelineMedia return statuses from media timeline.
// NOTE: This is an experimental feature of pawoo.net.
func (c *Client) GetTimelineMedia(ctx context.Context, isLocal bool, pg *Pagination) ([]*Status, error) {
	params := url.Values{}
	params.Set("media", "t")
	if isLocal {
		params.Set("local", "t")
	}

	var statuses []*Status
	err := c.doAPI(ctx, http.MethodGet, "/api/v1/timelines/public", params, &statuses, pg)
	if err != nil {
		return nil, err
	}
	return statuses, nil
}

// PostStatus post the toot.
func (c *Client) PostStatus(ctx context.Context, toot *Toot) (*Status, error) {
	params := url.Values{}
	params.Set("status", toot.Status)
	if toot.InReplyToID != "" {
		params.Set("in_reply_to_id", string(toot.InReplyToID))
	}
	if toot.MediaIDs != nil {
		for _, media := range toot.MediaIDs {
			params.Add("media_ids[]", string(media))
		}
	}
	if toot.Visibility != "" {
		params.Set("visibility", fmt.Sprint(toot.Visibility))
	}
	if toot.Sensitive {
		params.Set("sensitive", "true")
	}
	if toot.SpoilerText != "" {
		params.Set("spoiler_text", toot.SpoilerText)
	}

	var status Status
	err := c.doAPI(ctx, http.MethodPost, "/api/v1/statuses", params, &status, nil)
	if err != nil {
		return nil, err
	}
	return &status, nil
}

// DeleteStatus delete the toot.
func (c *Client) DeleteStatus(ctx context.Context, id ID) error {
	return c.doAPI(ctx, http.MethodDelete, fmt.Sprintf("/api/v1/statuses/%s", id), nil, nil, nil)
}

// Search search content with query.
func (c *Client) Search(ctx context.Context, q string, resolve bool) (*Results, error) {
	params := url.Values{}
	params.Set("q", q)
	params.Set("resolve", fmt.Sprint(resolve))
	var results Results
	err := c.doAPI(ctx, http.MethodGet, "/api/v1/search", params, &results, nil)
	if err != nil {
		return nil, err
	}
	return &results, nil
}

// UploadMedia upload a media attachment from a file.
func (c *Client) UploadMedia(ctx context.Context, file string) (*Attachment, error) {
	var attachment Attachment
	err := c.doAPI(ctx, http.MethodPost, "/api/v1/media", file, &attachment, nil)
	if err != nil {
		return nil, err
	}
	return &attachment, nil
}

// UploadMediaFromReader uploads a media attachment from a io.Reader.
func (c *Client) UploadMediaFromReader(ctx context.Context, reader io.Reader) (*Attachment, error) {
	var attachment Attachment
	err := c.doAPI(ctx, http.MethodPost, "/api/v1/media", reader, &attachment, nil)
	if err != nil {
		return nil, err
	}
	return &attachment, nil
}
