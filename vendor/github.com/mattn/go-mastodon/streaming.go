package mastodon

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
)

// UpdateEvent is struct for passing status event to app.
type UpdateEvent struct {
	Status *Status `json:"status"`
}

func (e *UpdateEvent) event() {}

// NotificationEvent is struct for passing notification event to app.
type NotificationEvent struct {
	Notification *Notification `json:"notification"`
}

func (e *NotificationEvent) event() {}

// DeleteEvent is struct for passing deletion event to app.
type DeleteEvent struct{ ID ID }

func (e *DeleteEvent) event() {}

// ErrorEvent is struct for passing errors to app.
type ErrorEvent struct{ err error }

func (e *ErrorEvent) event()        {}
func (e *ErrorEvent) Error() string { return e.err.Error() }

// Event is interface passing events to app.
type Event interface {
	event()
}

func handleReader(q chan Event, r io.Reader) error {
	var name string
	s := bufio.NewScanner(r)
	for s.Scan() {
		line := s.Text()
		token := strings.SplitN(line, ":", 2)
		if len(token) != 2 {
			continue
		}
		switch strings.TrimSpace(token[0]) {
		case "event":
			name = strings.TrimSpace(token[1])
		case "data":
			var err error
			switch name {
			case "update":
				var status Status
				err = json.Unmarshal([]byte(token[1]), &status)
				if err == nil {
					q <- &UpdateEvent{&status}
				}
			case "notification":
				var notification Notification
				err = json.Unmarshal([]byte(token[1]), &notification)
				if err == nil {
					q <- &NotificationEvent{&notification}
				}
			case "delete":
				q <- &DeleteEvent{ID: ID(strings.TrimSpace(token[1]))}
			}
			if err != nil {
				q <- &ErrorEvent{err}
			}
		}
	}
	return s.Err()
}

func (c *Client) streaming(ctx context.Context, p string, params url.Values) (chan Event, error) {
	u, err := url.Parse(c.config.Server)
	if err != nil {
		return nil, err
	}
	u.Path = path.Join(u.Path, "/api/v1/streaming", p)
	u.RawQuery = params.Encode()

	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req = req.WithContext(ctx)
	req.Header.Set("Authorization", "Bearer "+c.config.AccessToken)

	q := make(chan Event)
	go func() {
		defer close(q)
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			c.doStreaming(req, q)
		}
	}()
	return q, nil
}

func (c *Client) doStreaming(req *http.Request, q chan Event) {
	resp, err := c.Do(req)
	if err != nil {
		q <- &ErrorEvent{err}
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		q <- &ErrorEvent{parseAPIError("bad request", resp)}
		return
	}

	err = handleReader(q, resp.Body)
	if err != nil {
		q <- &ErrorEvent{err}
	}
}

// StreamingUser return channel to read events on home.
func (c *Client) StreamingUser(ctx context.Context) (chan Event, error) {
	return c.streaming(ctx, "user", nil)
}

// StreamingPublic return channel to read events on public.
func (c *Client) StreamingPublic(ctx context.Context, isLocal bool) (chan Event, error) {
	p := "public"
	if isLocal {
		p = path.Join(p, "local")
	}

	return c.streaming(ctx, p, nil)
}

// StreamingHashtag return channel to read events on tagged timeline.
func (c *Client) StreamingHashtag(ctx context.Context, tag string, isLocal bool) (chan Event, error) {
	params := url.Values{}
	params.Set("tag", tag)

	p := "hashtag"
	if isLocal {
		p = path.Join(p, "local")
	}

	return c.streaming(ctx, p, params)
}

// StreamingList return channel to read events on a list.
func (c *Client) StreamingList(ctx context.Context, id ID) (chan Event, error) {
	params := url.Values{}
	params.Set("list", string(id))

	return c.streaming(ctx, "list", params)
}
