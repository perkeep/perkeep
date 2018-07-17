package mastodon

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// Notification hold information for mastodon notification.
type Notification struct {
	ID        ID        `json:"id"`
	Type      string    `json:"type"`
	CreatedAt time.Time `json:"created_at"`
	Account   Account   `json:"account"`
	Status    *Status   `json:"status"`
}

// GetNotifications return notifications.
func (c *Client) GetNotifications(ctx context.Context, pg *Pagination) ([]*Notification, error) {
	var notifications []*Notification
	err := c.doAPI(ctx, http.MethodGet, "/api/v1/notifications", nil, &notifications, pg)
	if err != nil {
		return nil, err
	}
	return notifications, nil
}

// GetNotification return notification.
func (c *Client) GetNotification(ctx context.Context, id ID) (*Notification, error) {
	var notification Notification
	err := c.doAPI(ctx, http.MethodGet, fmt.Sprintf("/api/v1/notifications/%v", id), nil, &notification, nil)
	if err != nil {
		return nil, err
	}
	return &notification, nil
}

// ClearNotifications clear notifications.
func (c *Client) ClearNotifications(ctx context.Context) error {
	return c.doAPI(ctx, http.MethodPost, "/api/v1/notifications/clear", nil, nil, nil)
}
