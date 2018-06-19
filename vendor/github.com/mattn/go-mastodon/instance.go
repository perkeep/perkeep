package mastodon

import (
	"context"
	"net/http"
)

// Instance hold information for mastodon instance.
type Instance struct {
	URI         string            `json:"uri"`
	Title       string            `json:"title"`
	Description string            `json:"description"`
	EMail       string            `json:"email"`
	Version     string            `json:"version,omitempty"`
	URLs        map[string]string `json:"urls,omitempty"`
	Stats       *InstanceStats    `json:"stats,omitempty"`
	Thumbnail   string            `json:"thumbnail,omitempty"`
}

// InstanceStats hold information for mastodon instance stats.
type InstanceStats struct {
	UserCount   int64 `json:"user_count"`
	StatusCount int64 `json:"status_count"`
	DomainCount int64 `json:"domain_count"`
}

// GetInstance return Instance.
func (c *Client) GetInstance(ctx context.Context) (*Instance, error) {
	var instance Instance
	err := c.doAPI(ctx, http.MethodGet, "/api/v1/instance", nil, &instance, nil)
	if err != nil {
		return nil, err
	}
	return &instance, nil
}

// WeeklyActivity hold information for mastodon weekly activity.
type WeeklyActivity struct {
	Week          Unixtime `json:"week"`
	Statuses      int64    `json:"statuses,string"`
	Logins        int64    `json:"logins,string"`
	Registrations int64    `json:"registrations,string"`
}

// GetInstanceActivity return instance activity.
func (c *Client) GetInstanceActivity(ctx context.Context) ([]*WeeklyActivity, error) {
	var activity []*WeeklyActivity
	err := c.doAPI(ctx, http.MethodGet, "/api/v1/instance/activity", nil, &activity, nil)
	if err != nil {
		return nil, err
	}
	return activity, nil
}

// GetInstancePeers return instance peers.
func (c *Client) GetInstancePeers(ctx context.Context) ([]string, error) {
	var peers []string
	err := c.doAPI(ctx, http.MethodGet, "/api/v1/instance/peers", nil, &peers, nil)
	if err != nil {
		return nil, err
	}
	return peers, nil
}
