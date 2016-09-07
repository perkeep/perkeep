// Copyright 2016 Google Inc. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// AUTO-GENERATED CODE. DO NOT EDIT.

package pubsub

import (
	"fmt"
	"runtime"
	"time"

	gax "github.com/googleapis/gax-go"
	"golang.org/x/net/context"
	"google.golang.org/api/option"
	"google.golang.org/api/transport"
	googleapis_pubsub_v1 "google.golang.org/genproto/googleapis/pubsub/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
)

var (
	subscriberProjectPathTemplate      = gax.MustCompilePathTemplate("projects/{project}")
	subscriberSubscriptionPathTemplate = gax.MustCompilePathTemplate("projects/{project}/subscriptions/{subscription}")
	subscriberTopicPathTemplate        = gax.MustCompilePathTemplate("projects/{project}/topics/{topic}")
)

// SubscriberCallOptions contains the retry settings for each method of this client.
type SubscriberCallOptions struct {
	CreateSubscription []gax.CallOption
	GetSubscription    []gax.CallOption
	ListSubscriptions  []gax.CallOption
	DeleteSubscription []gax.CallOption
	ModifyAckDeadline  []gax.CallOption
	Acknowledge        []gax.CallOption
	Pull               []gax.CallOption
	ModifyPushConfig   []gax.CallOption
}

func defaultSubscriberClientOptions() []option.ClientOption {
	return []option.ClientOption{
		option.WithEndpoint("pubsub.googleapis.com:443"),
		option.WithScopes(
			"https://www.googleapis.com/auth/cloud-platform",
			"https://www.googleapis.com/auth/pubsub",
		),
	}
}

func defaultSubscriberRetryOptions() []gax.CallOption {
	return []gax.CallOption{
		gax.WithTimeout(600000 * time.Millisecond),
		gax.WithDelayTimeoutSettings(100*time.Millisecond, 60000*time.Millisecond, 1.3),
		gax.WithRPCTimeoutSettings(60000*time.Millisecond, 60000*time.Millisecond, 1.0),
	}
}
func messagingSubscriberRetryOptions() []gax.CallOption {
	return []gax.CallOption{
		gax.WithTimeout(600000 * time.Millisecond),
		gax.WithDelayTimeoutSettings(100*time.Millisecond, 60000*time.Millisecond, 1.3),
		gax.WithRPCTimeoutSettings(12000*time.Millisecond, 12000*time.Millisecond, 1.0),
	}
}

func defaultSubscriberCallOptions() *SubscriberCallOptions {
	withIdempotentRetryCodes := gax.WithRetryCodes([]codes.Code{
		codes.DeadlineExceeded,
		codes.Unavailable,
	},
	)
	return &SubscriberCallOptions{
		CreateSubscription: append(defaultSubscriberRetryOptions(), withIdempotentRetryCodes),
		GetSubscription:    append(defaultSubscriberRetryOptions(), withIdempotentRetryCodes),
		ListSubscriptions:  append(defaultSubscriberRetryOptions(), withIdempotentRetryCodes),
		DeleteSubscription: append(defaultSubscriberRetryOptions(), withIdempotentRetryCodes),
		ModifyAckDeadline:  defaultSubscriberRetryOptions(),
		Acknowledge:        messagingSubscriberRetryOptions(),
		Pull:               messagingSubscriberRetryOptions(),
		ModifyPushConfig:   defaultSubscriberRetryOptions(),
	}
}

// SubscriberClient is a client for interacting with Subscriber.
type SubscriberClient struct {
	// The connection to the service.
	conn *grpc.ClientConn

	// The gRPC API client.
	client googleapis_pubsub_v1.SubscriberClient

	// The call options for this service.
	CallOptions *SubscriberCallOptions

	// The metadata to be sent with each request.
	metadata map[string][]string
}

// NewSubscriberClient creates a new subscriber service client.
//
// The service that an application uses to manipulate subscriptions and to
// consume messages from a subscription via the `Pull` method.
func NewSubscriberClient(ctx context.Context, opts ...option.ClientOption) (*SubscriberClient, error) {
	conn, err := transport.DialGRPC(ctx, append(defaultSubscriberClientOptions(), opts...)...)
	if err != nil {
		return nil, err
	}
	c := &SubscriberClient{
		conn:        conn,
		client:      googleapis_pubsub_v1.NewSubscriberClient(conn),
		CallOptions: defaultSubscriberCallOptions(),
	}
	c.SetGoogleClientInfo("gax", gax.Version)
	return c, nil
}

// Connection returns the client's connection to the API service.
func (c *SubscriberClient) Connection() *grpc.ClientConn {
	return c.conn
}

// Close closes the connection to the API service. The user should invoke this when
// the client is no longer required.
func (c *SubscriberClient) Close() error {
	return c.conn.Close()
}

// SetGoogleClientInfo sets the name and version of the application in
// the `x-goog-api-client` header passed on each request. Intended for
// use by Google-written clients.
func (c *SubscriberClient) SetGoogleClientInfo(name, version string) {
	c.metadata = map[string][]string{
		"x-goog-api-client": {fmt.Sprintf("%s/%s %s gax/%s go/%s", name, version, gapicNameVersion, gax.Version, runtime.Version())},
	}
}

// Path templates.

// ProjectPath returns the path for the project resource.
func SubscriberProjectPath(project string) string {
	path, err := subscriberProjectPathTemplate.Render(map[string]string{
		"project": project,
	})
	if err != nil {
		panic(err)
	}
	return path
}

// SubscriptionPath returns the path for the subscription resource.
func SubscriberSubscriptionPath(project string, subscription string) string {
	path, err := subscriberSubscriptionPathTemplate.Render(map[string]string{
		"project":      project,
		"subscription": subscription,
	})
	if err != nil {
		panic(err)
	}
	return path
}

// TopicPath returns the path for the topic resource.
func SubscriberTopicPath(project string, topic string) string {
	path, err := subscriberTopicPathTemplate.Render(map[string]string{
		"project": project,
		"topic":   topic,
	})
	if err != nil {
		panic(err)
	}
	return path
}

// CreateSubscription creates a subscription to a given topic for a given subscriber.
// If the subscription already exists, returns `ALREADY_EXISTS`.
// If the corresponding topic doesn't exist, returns `NOT_FOUND`.
//
// If the name is not provided in the request, the server will assign a random
// name for this subscription on the same project as the topic.
func (c *SubscriberClient) CreateSubscription(ctx context.Context, req *googleapis_pubsub_v1.Subscription) (*googleapis_pubsub_v1.Subscription, error) {
	ctx = metadata.NewContext(ctx, c.metadata)
	var resp *googleapis_pubsub_v1.Subscription
	err := gax.Invoke(ctx, func(ctx context.Context) error {
		var err error
		resp, err = c.client.CreateSubscription(ctx, req)
		return err
	}, c.CallOptions.CreateSubscription...)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// GetSubscription gets the configuration details of a subscription.
func (c *SubscriberClient) GetSubscription(ctx context.Context, req *googleapis_pubsub_v1.GetSubscriptionRequest) (*googleapis_pubsub_v1.Subscription, error) {
	ctx = metadata.NewContext(ctx, c.metadata)
	var resp *googleapis_pubsub_v1.Subscription
	err := gax.Invoke(ctx, func(ctx context.Context) error {
		var err error
		resp, err = c.client.GetSubscription(ctx, req)
		return err
	}, c.CallOptions.GetSubscription...)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// ListSubscriptions lists matching subscriptions.
func (c *SubscriberClient) ListSubscriptions(ctx context.Context, req *googleapis_pubsub_v1.ListSubscriptionsRequest) *SubscriptionIterator {
	ctx = metadata.NewContext(ctx, c.metadata)
	it := &SubscriptionIterator{}
	it.apiCall = func() error {
		var resp *googleapis_pubsub_v1.ListSubscriptionsResponse
		err := gax.Invoke(ctx, func(ctx context.Context) error {
			var err error
			req.PageToken = it.nextPageToken
			resp, err = c.client.ListSubscriptions(ctx, req)
			return err
		}, c.CallOptions.ListSubscriptions...)
		if err != nil {
			return err
		}
		if resp.NextPageToken == "" {
			it.atLastPage = true
		}
		it.nextPageToken = resp.NextPageToken
		it.items = resp.Subscriptions
		return nil
	}
	return it
}

// DeleteSubscription deletes an existing subscription. All pending messages in the subscription
// are immediately dropped. Calls to `Pull` after deletion will return
// `NOT_FOUND`. After a subscription is deleted, a new one may be created with
// the same name, but the new one has no association with the old
// subscription, or its topic unless the same topic is specified.
func (c *SubscriberClient) DeleteSubscription(ctx context.Context, req *googleapis_pubsub_v1.DeleteSubscriptionRequest) error {
	ctx = metadata.NewContext(ctx, c.metadata)
	err := gax.Invoke(ctx, func(ctx context.Context) error {
		var err error
		_, err = c.client.DeleteSubscription(ctx, req)
		return err
	}, c.CallOptions.DeleteSubscription...)
	return err
}

// ModifyAckDeadline modifies the ack deadline for a specific message. This method is useful
// to indicate that more time is needed to process a message by the
// subscriber, or to make the message available for redelivery if the
// processing was interrupted.
func (c *SubscriberClient) ModifyAckDeadline(ctx context.Context, req *googleapis_pubsub_v1.ModifyAckDeadlineRequest) error {
	ctx = metadata.NewContext(ctx, c.metadata)
	err := gax.Invoke(ctx, func(ctx context.Context) error {
		var err error
		_, err = c.client.ModifyAckDeadline(ctx, req)
		return err
	}, c.CallOptions.ModifyAckDeadline...)
	return err
}

// Acknowledge acknowledges the messages associated with the `ack_ids` in the
// `AcknowledgeRequest`. The Pub/Sub system can remove the relevant messages
// from the subscription.
//
// Acknowledging a message whose ack deadline has expired may succeed,
// but such a message may be redelivered later. Acknowledging a message more
// than once will not result in an error.
func (c *SubscriberClient) Acknowledge(ctx context.Context, req *googleapis_pubsub_v1.AcknowledgeRequest) error {
	ctx = metadata.NewContext(ctx, c.metadata)
	err := gax.Invoke(ctx, func(ctx context.Context) error {
		var err error
		_, err = c.client.Acknowledge(ctx, req)
		return err
	}, c.CallOptions.Acknowledge...)
	return err
}

// Pull pulls messages from the server. Returns an empty list if there are no
// messages available in the backlog. The server may return `UNAVAILABLE` if
// there are too many concurrent pull requests pending for the given
// subscription.
func (c *SubscriberClient) Pull(ctx context.Context, req *googleapis_pubsub_v1.PullRequest) (*googleapis_pubsub_v1.PullResponse, error) {
	ctx = metadata.NewContext(ctx, c.metadata)
	var resp *googleapis_pubsub_v1.PullResponse
	err := gax.Invoke(ctx, func(ctx context.Context) error {
		var err error
		resp, err = c.client.Pull(ctx, req)
		return err
	}, c.CallOptions.Pull...)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// ModifyPushConfig modifies the `PushConfig` for a specified subscription.
//
// This may be used to change a push subscription to a pull one (signified by
// an empty `PushConfig`) or vice versa, or change the endpoint URL and other
// attributes of a push subscription. Messages will accumulate for delivery
// continuously through the call regardless of changes to the `PushConfig`.
func (c *SubscriberClient) ModifyPushConfig(ctx context.Context, req *googleapis_pubsub_v1.ModifyPushConfigRequest) error {
	ctx = metadata.NewContext(ctx, c.metadata)
	err := gax.Invoke(ctx, func(ctx context.Context) error {
		var err error
		_, err = c.client.ModifyPushConfig(ctx, req)
		return err
	}, c.CallOptions.ModifyPushConfig...)
	return err
}

// Iterators.
//

// SubscriptionIterator manages a stream of *googleapis_pubsub_v1.Subscription.
type SubscriptionIterator struct {
	// The current page data.
	items         []*googleapis_pubsub_v1.Subscription
	atLastPage    bool
	currentIndex  int
	nextPageToken string
	apiCall       func() error
}

// NextPage returns the next page of results.
//
// NextPage may return a second return value of Done along with the last page of results. After
// NextPage returns Done, all subsequent calls to NextPage will return (nil, Done).
//
// Next and NextPage should not be used with the same iterator.
func (it *SubscriptionIterator) NextPage() ([]*googleapis_pubsub_v1.Subscription, error) {
	if it.atLastPage {
		// We already returned Done with the last page of items. Continue to
		// return Done, but with no items.
		return nil, Done
	}
	if err := it.apiCall(); err != nil {
		return nil, err
	}
	if it.atLastPage {
		return it.items, Done
	}
	return it.items, nil
}

// Next returns the next result. Its second return value is Done if there are no more results.
// Once next returns Done, all subsequent calls will return Done.
//
// SetPageToken should not be called when using Next.
//
// Next and NextPage should not be used with the same iterator.
func (it *SubscriptionIterator) Next() (*googleapis_pubsub_v1.Subscription, error) {
	for it.currentIndex >= len(it.items) {
		if it.atLastPage {
			return nil, Done
		}
		if err := it.apiCall(); err != nil {
			return nil, err
		}
		it.currentIndex = 0
	}
	result := it.items[it.currentIndex]
	it.currentIndex++
	return result, nil
}

// SetPageToken sets the page token for the next call to NextPage, to resume the iteration from
// a previous point.
func (it *SubscriptionIterator) SetPageToken(token string) {
	it.nextPageToken = token
}

// NextPageToken returns a page token that can be used with SetPageToken to resume
// iteration from the next page. It returns the empty string if there are no more pages.
func (it *SubscriptionIterator) NextPageToken() string {
	return it.nextPageToken
}
