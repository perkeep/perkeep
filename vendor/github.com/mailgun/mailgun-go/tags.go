package mailgun

import (
	"net/url"
	"strconv"
	"time"
)

type TagItem struct {
	Value       string     `json:"tag"`
	Description string     `json:"description"`
	FirstSeen   *time.Time `json:"first-seen,omitempty"`
	LastSeen    *time.Time `json:"last-seen,omitempty"`
}

type TagsPage struct {
	Items  []TagItem `json:"items"`
	Paging Paging    `json:"paging"`
}

type TagOptions struct {
	// Restrict the page size to this limit
	Limit int
	// Return only the tags starting with the given prefix
	Prefix string
	// The page direction based off the 'tag' parameter; valid choices are (first, last, next, prev)
	Page string
	// The tag that marks piviot point for the 'page' parameter
	Tag string
}

// DeleteTag removes all counters for a particular tag, including the tag itself.
func (m *MailgunImpl) DeleteTag(tag string) error {
	r := newHTTPRequest(generateApiUrl(m, tagsEndpoint) + "/" + tag)
	r.setClient(m.Client())
	r.setBasicAuth(basicAuthUser, m.ApiKey())
	_, err := makeDeleteRequest(r)
	return err
}

// GetTag retrieves metadata about the tag from the api
func (m *MailgunImpl) GetTag(tag string) (TagItem, error) {
	r := newHTTPRequest(generateApiUrl(m, tagsEndpoint) + "/" + tag)
	r.setClient(m.Client())
	r.setBasicAuth(basicAuthUser, m.ApiKey())
	var tagItem TagItem
	return tagItem, getResponseFromJSON(r, &tagItem)
}

// ListTags returns a cursor used to iterate through a list of tags
//	it := mg.ListTags(nil)
//	var page TagsPage
//	for it.Next(&page) {
//		for _, tag := range(page.Items) {
//			// Do stuff with tags
//		}
//	}
//	if it.Err() != nil {
//		log.Fatal(it.Err())
//	}
func (m *MailgunImpl) ListTags(opts *TagOptions) *TagIterator {
	req := newHTTPRequest(generateApiUrl(m, tagsEndpoint))
	if opts != nil {
		if opts.Limit != 0 {
			req.addParameter("limit", strconv.Itoa(opts.Limit))
		}
		if opts.Prefix != "" {
			req.addParameter("prefix", opts.Prefix)
		}
		if opts.Page != "" {
			req.addParameter("page", opts.Page)
		}
		if opts.Tag != "" {
			req.addParameter("tag", opts.Tag)
		}
	}

	initialUrl, _ := req.generateUrlWithParameters()
	tagPage := TagsPage{
		Paging: Paging{
			First:    initialUrl,
			Next:     initialUrl,
			Last:     initialUrl,
			Previous: initialUrl,
		},
	}
	return NewTagCursor(tagPage, m)
}

type TagIterator struct {
	mg   Mailgun
	curr TagsPage
	err  error
}

// Creates a new cursor from a taglist
func NewTagCursor(tagPage TagsPage, mailgun Mailgun) *TagIterator {
	return &TagIterator{curr: tagPage, mg: mailgun}
}

// Returns the next page in the list of tags
func (t *TagIterator) Next(tagPage *TagsPage) bool {
	if !canFetchPage(t.curr.Paging.Next) {
		return false
	}

	if err := t.cursorRequest(tagPage, t.curr.Paging.Next); err != nil {
		t.err = err
		return false
	}
	t.curr = *tagPage
	return true
}

// Returns the previous page in the list of tags
func (t *TagIterator) Previous(tagPage *TagsPage) bool {
	if !canFetchPage(t.curr.Paging.Previous) {
		return false
	}

	if err := t.cursorRequest(tagPage, t.curr.Paging.Previous); err != nil {
		t.err = err
		return false
	}
	t.curr = *tagPage
	return true
}

// Returns the first page in the list of tags
func (t *TagIterator) First(tagPage *TagsPage) bool {
	if err := t.cursorRequest(tagPage, t.curr.Paging.First); err != nil {
		t.err = err
		return false
	}
	t.curr = *tagPage
	return true
}

// Returns the last page in the list of tags
func (t *TagIterator) Last(tagPage *TagsPage) bool {
	if err := t.cursorRequest(tagPage, t.curr.Paging.Last); err != nil {
		t.err = err
		return false
	}
	t.curr = *tagPage
	return true
}

// Return any error if one occurred
func (t *TagIterator) Err() error {
	return t.err
}

func (t *TagIterator) cursorRequest(tagPage *TagsPage, url string) error {
	req := newHTTPRequest(url)
	req.setClient(t.mg.Client())
	req.setBasicAuth(basicAuthUser, t.mg.ApiKey())
	return getResponseFromJSON(req, tagPage)
}

func canFetchPage(slug string) bool {
	parts, err := url.Parse(slug)
	if err != nil {
		return false
	}
	params, _ := url.ParseQuery(parts.RawQuery)
	if err != nil {
		return false
	}
	value, ok := params["tag"]
	// If tags doesn't exist, it's our first time fetching pages
	if !ok {
		return true
	}
	// If tags has no value, there are no more pages to fetch
	return len(value) == 0
}
