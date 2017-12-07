package mailgun

import (
	"strconv"
	"time"
)

const iso8601date = "2006-01-02"

type Stat struct {
	Event      string         `json:"event"`
	TotalCount int            `json:"total_count"`
	CreatedAt  string         `json:"created_at"`
	Id         string         `json:"id"`
	Tags       map[string]int `json:"tags"`
}

type statsEnvelope struct {
	TotalCount int    `json:"total_count"`
	Items      []Stat `json:"items"`
}

type Accepted struct {
	Incoming int `json:"incoming"`
	Outgoing int `json:"outgoing"`
	Total    int `json:"total"`
}

type Delivered struct {
	Smtp  int `json:"smtp"`
	Http  int `json:"http"`
	Total int `json:"total"`
}

type Temporary struct {
	Espblock int `json:"espblock"`
}

type Permanent struct {
	SuppressBounce      int `json:"suppress-bounce"`
	SuppressUnsubscribe int `json:"suppress-unsubscribe"`
	SuppressComplaint   int `json:"suppress-complaint"`
	Bounce              int `json:"bounce"`
	DelayedBounce       int `json:"delayed-bounce"`
	Total               int `json:"total"`
}

type Failed struct {
	Temporary Temporary `json:"temporary"`
	Permanent Permanent `json:"permanent"`
}

type Total struct {
	Total int `json:"total"`
}

type StatsTotal struct {
	Time         string    `json:"time"`
	Accepted     Accepted  `json:"accepted"`
	Delivered    Delivered `json:"delivered"`
	Failed       Failed    `json:"failed"`
	Stored       Total     `json:"stored"`
	Opened       Total     `json:"opened"`
	Clicked      Total     `json:"clicked"`
	Unsubscribed Total     `json:"unsubscribed"`
	Complained   Total     `json:"complained"`
}

type StatsTotalResponse struct {
	End        string       `json:"end"`
	Resolution string       `json:"resolution"`
	Start      string       `json:"start"`
	Stats      []StatsTotal `json:"stats"`
}

// GetStats returns a basic set of statistics for different events.
// Events start at the given start date, if one is provided.
// If not, this function will consider all stated events dating to the creation of the sending domain.
func (m *MailgunImpl) GetStats(limit int, skip int, startDate *time.Time, event ...string) (int, []Stat, error) {
	r := newHTTPRequest(generateApiUrl(m, statsEndpoint))

	if limit != -1 {
		r.addParameter("limit", strconv.Itoa(limit))
	}
	if skip != -1 {
		r.addParameter("skip", strconv.Itoa(skip))
	}

	if startDate != nil {
		r.addParameter("start-date", startDate.Format(iso8601date))
	}

	for _, e := range event {
		r.addParameter("event", e)
	}
	r.setClient(m.Client())
	r.setBasicAuth(basicAuthUser, m.ApiKey())

	var res statsEnvelope
	err := getResponseFromJSON(r, &res)
	if err != nil {
		return -1, nil, err
	} else {
		return res.TotalCount, res.Items, nil
	}
}

// GetStatsTotal returns a basic set of statistics for different events.
// https://documentation.mailgun.com/en/latest/api-stats.html#stats
func (m *MailgunImpl) GetStatsTotal(start *time.Time, end *time.Time, resolution string, duration string, event ...string) (*StatsTotalResponse, error) {
	r := newHTTPRequest(generateApiUrl(m, statsTotalEndpoint))

	if start != nil {
		r.addParameter("start", start.Format(iso8601date))
	}
	if end != nil {
		r.addParameter("end", end.Format(iso8601date))
	}

	if resolution != "" {
		r.addParameter("resolution", resolution)
	}

	if duration != "" {
		r.addParameter("duration", duration)
	}

	for _, e := range event {
		r.addParameter("event", e)
	}
	r.setClient(m.Client())
	r.setBasicAuth(basicAuthUser, m.ApiKey())

	var res StatsTotalResponse
	err := getResponseFromJSON(r, &res)
	if err != nil {
		return nil, err
	} else {
		return &res, nil
	}
}
