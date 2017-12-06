package mailgun

import (
	"fmt"
	"time"

	"github.com/pkg/errors"
)

type eventResponse struct {
	Events []Event `json:"items"`
	Paging Paging  `json:"paging"`
}

type Event struct {
	// Mandatory fields present in each event
	ID        string        `json:"id"`
	Timestamp TimestampNano `json:"timestamp"`
	Event     EventType     `json:"event"`

	// Delivery related values
	DeliveryStatus *DeliveryStatus `json:"delivery-status,omitempty"`
	Reason         *EventReason    `json:"reason,omitempty"`
	Severity       *EventSeverity  `json:"severity,omitempty"`

	// Message classification / grouping
	Tags      []string   `json:"tags,omitempty"`
	Campaigns []Campaign `json:"campaigns,omitempty"`

	// Recipient information (for recipient-initiated events: opens, clicks etc)
	ClientInfo  *ClientInfo  `json:"client-info,omitempty"`
	Geolocation *Geolocation `json:"geolocation,omitempty"`
	IP          *IP          `json:"ip,omitempty"`
	Envelope    *Envelope    `json:"envelope,omitempty"`

	// Clicked
	URL *string `json:"url,omitempty"`

	// Message
	// TODO: unify message types
	Message       *EventMessage     `json:"message,omitempty"`
	Batch         *Batch            `json:"batch,omitempty"`
	Recipient     *Recipient        `json:"recipient,omitempty"`
	Routes        []Route           `json:"routes,omitempty"`
	Storage       *Storage          `json:"storage,omitempty"`
	UserVariables map[string]string `json:"user-variables"`

	// API
	Method *Method     `json:"method,omitempty"`
	Flags  *EventFlags `json:"flags,omitempty"`
}

type DeliveryStatus struct {
	Message     *string     `json:"message,omitempty"`
	Code        interface{} `json:"code,omitempty"`
	Description *string     `json:"description,omitempty"`
	Retry       *int        `json:"retry-seconds,omitempty"`
}

type EventFlags struct {
	Authenticated bool `json:"is-authenticated"`
	Batch         bool `json:"is-batch"`
	Big           bool `json:"is-big"`
	Callback      bool `json:"is-callback"`
	DelayedBounce bool `json:"is-delayed-bounce"`
	SystemTest    bool `json:"is-system-test"`
	TestMode      bool `json:"is-test-mode"`
}

type ClientInfo struct {
	ClientType *ClientType `json:"client-type,omitempty"`
	ClientOS   *string     `json:"client-os,omitempty"`
	ClientName *string     `json:"client-name,omitempty"`
	DeviceType *DeviceType `json:"device-type,omitempty"`
	UserAgent  *string     `json:"user-agent,omitempty"`
}

type Geolocation struct {
	Country *string `json:"country,omitempty"`
	Region  *string `json:"region,omitempty"`
	City    *string `json:"city,omitempty"`
}

type Storage struct {
	URL string `json:"url"`
	Key string `json:"key"`
}

type Batch struct {
	ID string `json:"id"`
}

type Envelope struct {
	Sender      *string          `json:"sender,omitempty"`
	SendingHost *string          `json:"sending-host,omitempty"`
	SendingIP   *IP              `json:"sending-ip,omitempty"`
	Targets     *string          `json:"targets,omitempty"`
	Transport   *TransportMethod `json:"transport,omitempty"`
}

type EventMessage struct {
	Headers     map[string]string  `json:"headers,omitempty"`
	Recipients  []string           `json:"recipients,omitempty"`
	Attachments []StoredAttachment `json:"attachments,omitempty"`
	Size        *int               `json:"size,omitempty"`
}

func (em *EventMessage) ID() (string, error) {
	if em != nil && em.Headers != nil {
		if id, ok := em.Headers["message-id"]; ok {
			return id, nil
		}
	}
	return "", errors.New("message id not set")
}

// GetEventsOptions lets the caller of GetEvents() specify how the results are to be returned.
// Begin and End time-box the results returned.
// ForceAscending and ForceDescending are used to force Mailgun to use a given traversal order of the events.
// If both ForceAscending and ForceDescending are true, an error will result.
// If none, the default will be inferred from the Begin and End parameters.
// Limit caps the number of results returned.  If left unspecified, Mailgun assumes 100.
// Compact, if true, compacts the returned JSON to minimize transmission bandwidth.
// Otherwise, the JSON is spaced appropriately for human consumption.
// Filter allows the caller to provide more specialized filters on the query.
// Consult the Mailgun documentation for more details.
type EventsOptions struct {
	Begin, End                               *time.Time
	ForceAscending, ForceDescending, Compact bool
	Limit                                    int
	Filter                                   map[string]string
	ThresholdAge                             time.Duration
	PollInterval                             time.Duration
}

// Depreciated See `ListEvents()`
type GetEventsOptions struct {
	Begin, End                               *time.Time
	ForceAscending, ForceDescending, Compact bool
	Limit                                    int
	Filter                                   map[string]string
}

// EventIterator maintains the state necessary for paging though small parcels of a larger set of events.
type EventIterator struct {
	eventResponse
	mg  Mailgun
	err error
}

// NewEventIterator creates a new iterator for events.
// Use GetFirstPage to retrieve the first batch of events.
// Use GetNext and GetPrevious thereafter as appropriate to iterate through sets of data.
//
// *This call is Deprecated, use ListEvents() instead*
func (mg *MailgunImpl) NewEventIterator() *EventIterator {
	return &EventIterator{mg: mg}
}

// Create an new iterator to fetch a page of events from the events api
//	it := mg.ListEvents(EventsOptions{})
//	var events []Event
//	for it.Next(&events) {
//	    	for _, event := range events {
//		        // Do things with events
//		}
//	}
//	if it.Err() != nil {
//		log.Fatal(it.Err())
//	}
func (mg *MailgunImpl) ListEvents(opts *EventsOptions) *EventIterator {
	req := newHTTPRequest(generateApiUrl(mg, eventsEndpoint))
	if opts != nil {
		if opts.Limit > 0 {
			req.addParameter("limit", fmt.Sprintf("%d", opts.Limit))
		}
		if opts.Compact {
			req.addParameter("pretty", "no")
		}
		if opts.ForceAscending {
			req.addParameter("ascending", "yes")
		} else if opts.ForceDescending {
			req.addParameter("ascending", "no")
		}
		if opts.Begin != nil {
			req.addParameter("begin", formatMailgunTime(opts.Begin))
		}
		if opts.End != nil {
			req.addParameter("end", formatMailgunTime(opts.End))
		}
		if opts.Filter != nil {
			for k, v := range opts.Filter {
				req.addParameter(k, v)
			}
		}
	}
	url, err := req.generateUrlWithParameters()
	return &EventIterator{
		mg:            mg,
		eventResponse: eventResponse{Paging: Paging{Next: url, First: url}},
		err:           err,
	}
}

// If an error occurred during iteration `Err()` will return non nil
func (ei *EventIterator) Err() error {
	return ei.err
}

// GetFirstPage retrieves the first batch of events, according to your criteria.
// See the GetEventsOptions structure for more details on how the fields affect the data returned.
func (ei *EventIterator) GetFirstPage(opts GetEventsOptions) error {
	if opts.ForceAscending && opts.ForceDescending {
		return fmt.Errorf("collation cannot at once be both ascending and descending")
	}

	payload := newUrlEncodedPayload()
	if opts.Limit != 0 {
		payload.addValue("limit", fmt.Sprintf("%d", opts.Limit))
	}
	if opts.Compact {
		payload.addValue("pretty", "no")
	}
	if opts.ForceAscending {
		payload.addValue("ascending", "yes")
	}
	if opts.ForceDescending {
		payload.addValue("ascending", "no")
	}
	if opts.Begin != nil {
		payload.addValue("begin", formatMailgunTime(opts.Begin))
	}
	if opts.End != nil {
		payload.addValue("end", formatMailgunTime(opts.End))
	}
	if opts.Filter != nil {
		for k, v := range opts.Filter {
			payload.addValue(k, v)
		}
	}

	url, err := generateParameterizedUrl(ei.mg, eventsEndpoint, payload)
	if err != nil {
		return err
	}
	return ei.fetch(url)
}

// Retrieves the chronologically previous batch of events, if any exist.
// You know you're at the end of the list when len(Events())==0.
func (ei *EventIterator) GetPrevious() error {
	return ei.fetch(ei.Paging.Previous)
}

// Retrieves the chronologically next batch of events, if any exist.
// You know you're at the end of the list when len(Events())==0.
func (ei *EventIterator) GetNext() error {
	return ei.fetch(ei.Paging.Next)
}

// Retrieves the next page of events from the api. Returns false when there
// no more pages to retrieve or if there was an error. Use `.Err()` to retrieve
// the error
func (ei *EventIterator) Next(events *[]Event) bool {
	if ei.err != nil {
		return false
	}
	ei.err = ei.fetch(ei.Paging.Next)
	if ei.err != nil {
		return false
	}
	*events = ei.Events
	if len(ei.Events) == 0 {
		return false
	}
	return true
}

// Retrieves the first page of events from the api. Returns false if there
// was an error. It also sets the iterator object to the first page.
// Use `.Err()` to retrieve the error.
func (ei *EventIterator) First(events *[]Event) bool {
	if ei.err != nil {
		return false
	}
	ei.err = ei.fetch(ei.Paging.First)
	if ei.err != nil {
		return false
	}
	*events = ei.Events
	return true
}

// Retrieves the last page of events from the api.
// Calling Last() is invalid unless you first call First() or Next()
// Returns false if there was an error. It also sets the iterator object
// to the last page. Use `.Err()` to retrieve the error.
func (ei *EventIterator) Last(events *[]Event) bool {
	if ei.err != nil {
		return false
	}
	ei.err = ei.fetch(ei.Paging.Last)
	if ei.err != nil {
		return false
	}
	*events = ei.Events
	return true
}

// Retrieves the previous page of events from the api. Returns false when there
// no more pages to retrieve or if there was an error. Use `.Err()` to retrieve
// the error if any
func (ei *EventIterator) Previous(events *[]Event) bool {
	if ei.err != nil {
		return false
	}
	if ei.Paging.Previous == "" {
		return false
	}
	ei.err = ei.fetch(ei.Paging.Previous)
	if ei.err != nil {
		return false
	}
	*events = ei.Events
	if len(ei.Events) == 0 {
		return false
	}
	return true
}

// EventPoller maintains the state necessary for polling events
type EventPoller struct {
	it            *EventIterator
	opts          EventsOptions
	thresholdTime time.Time
	sleepUntil    time.Time
	mg            Mailgun
	err           error
}

// Poll the events api and return new events as they occur
// 	it = mg.PollEvents(&EventsOptions{
//			// Poll() returns after this threshold is met, or events older than this threshold appear
// 			ThresholdAge: time.Second * 10,
//			// Only events with a timestamp after this date/time will be returned
//			Begin:        time.Now().Add(time.Second * -3),
//			// How often we poll the api for new events
//			PollInterval: time.Second * 4})
//	var events []Event
//	// Blocks until new events appear
//	for it.Poll(&events) {
//		for _, event := range(events) {
//			fmt.Printf("Event %+v\n", event)
//		}
//	}
//	if it.Err() != nil {
//		log.Fatal(it.Err())
//	}
func (mg *MailgunImpl) PollEvents(opts *EventsOptions) *EventPoller {
	now := time.Now()
	// ForceAscending must be set
	opts.ForceAscending = true

	// Default begin time is 30 minutes ago
	if opts.Begin == nil {
		t := now.Add(time.Minute * -30)
		opts.Begin = &t
	}

	// Default threshold age is 30 minutes
	if opts.ThresholdAge.Nanoseconds() == 0 {
		opts.ThresholdAge = time.Duration(time.Minute * 30)
	}

	// Set a 15 second poll interval if none set
	if opts.PollInterval.Nanoseconds() == 0 {
		opts.PollInterval = time.Duration(time.Second * 15)
	}

	return &EventPoller{
		it:   mg.ListEvents(opts),
		opts: *opts,
		mg:   mg,
	}
}

// If an error occurred during polling `Err()` will return non nil
func (ep *EventPoller) Err() error {
	return ep.err
}

func (ep *EventPoller) Poll(events *[]Event) bool {
	var currentPage string
	ep.thresholdTime = time.Now().UTC().Add(ep.opts.ThresholdAge)
	for {
		if !ep.sleepUntil.IsZero() {
			// Sleep the rest of our duration
			time.Sleep(ep.sleepUntil.Sub(time.Now()))
		}

		// Remember our current page url
		currentPage = ep.it.Paging.Next

		// Attempt to get a page of events
		var page []Event
		if ep.it.Next(&page) == false {
			if ep.it.Err() == nil && len(page) == 0 {
				// No events, sleep for our poll interval
				ep.sleepUntil = time.Now().Add(ep.opts.PollInterval)
				continue
			}
			ep.err = ep.it.Err()
			return false
		}

		// Last event on the page
		lastEvent := page[len(page)-1]

		timeStamp := time.Time(lastEvent.Timestamp)
		// Record the next time we should query for new events
		ep.sleepUntil = time.Now().Add(ep.opts.PollInterval)

		// If the last event on the page is older than our threshold time
		// or we have been polling for longer than our threshold time
		if timeStamp.After(ep.thresholdTime) || time.Now().UTC().After(ep.thresholdTime) {
			ep.thresholdTime = time.Now().UTC().Add(ep.opts.ThresholdAge)
			// Return the page of events to the user
			*events = page
			return true
		}
		// Since we didn't find an event older than our
		// threshold, fetch this same page again
		ep.it.Paging.Next = currentPage
	}
}

// GetFirstPage, GetPrevious, and GetNext all have a common body of code.
// fetch completes the API fetch common to all three of these functions.
func (ei *EventIterator) fetch(url string) error {
	r := newHTTPRequest(url)
	r.setClient(ei.mg.Client())
	r.setBasicAuth(basicAuthUser, ei.mg.ApiKey())

	return getResponseFromJSON(r, &ei.eventResponse)
}
