package raindrop

import "time"

type Bookmark struct {
	ID      int    `json:"_id"`
	Link    string `json:"link"`
	Title   string `json:"title"`
	Excerpt string `json:"excerpt"`
	Note    string `json:"note"`
	Type    string `json:"type"`
	User    struct {
		Ref string `json:"$ref"`
		ID  int    `json:"$id"`
	} `json:"user"`
	Cover string `json:"cover"`
	Media []struct {
		Type string `json:"type"`
		Link string `json:"link"`
	} `json:"media"`
	Tags       []string  `json:"tags"`
	Important  bool      `json:"important,omitempty"`
	Removed    bool      `json:"removed"`
	Created    time.Time `json:"created"`
	Highlights []any     `json:"highlights"`
	LastUpdate time.Time `json:"lastUpdate"`
	Domain     string    `json:"domain"`
	Collection struct {
		Ref string `json:"$ref"`
		ID  int    `json:"$id"`
		Oid int    `json:"oid"`
	} `json:"collection"`
	CreatorRef struct {
		ID     int    `json:"_id"`
		Avatar string `json:"avatar"`
		Name   string `json:"name"`
		Email  string `json:"email"`
	} `json:"creatorRef"`
	Sort         int `json:"sort"`
	CollectionID int `json:"collectionId"`
}

type User struct {
	Tfa struct {
		Enabled bool `json:"enabled"`
	} `json:"tfa"`
	Files struct {
		Used           int       `json:"used"`
		Size           int       `json:"size"`
		LastCheckPoint time.Time `json:"lastCheckPoint"`
	} `json:"files"`
	ID       int    `json:"_id"`
	Avatar   string `json:"avatar"`
	Pro      bool   `json:"pro"`
	Name     string `json:"name"`
	FullName string `json:"fullName"`
	Email    string `json:"email"`
	Groups   []struct {
		Title       string `json:"title"`
		Hidden      bool   `json:"hidden"`
		Sort        int    `json:"sort"`
		Collections []int  `json:"collections"`
	} `json:"groups"`
	LastAction time.Time `json:"lastAction"`
	LastVisit  time.Time `json:"lastVisit"`
	Registered time.Time `json:"registered"`
	LastUpdate time.Time `json:"lastUpdate"`
	Config     struct {
		RaindropsView               string   `json:"raindrops_view"`
		RaindropsHide               []string `json:"raindrops_hide"`
		RaindropsButtons            []string `json:"raindrops_buttons"`
		RaindropsSearchByScore      bool     `json:"raindrops_search_by_score"`
		BrokenLevel                 string   `json:"broken_level"`
		FontSize                    int      `json:"font_size"`
		AddDefaultCollection        int      `json:"add_default_collection"`
		Acknowledge                 []string `json:"acknowledge"`
		LastCollection              int      `json:"last_collection"`
		AiSuggestions               bool     `json:"ai_suggestions"`
		DefaultCollectionView       string   `json:"default_collection_view"`
		NestedViewLegacy            bool     `json:"nested_view_legacy"`
		RaindropsSearchIncollection bool     `json:"raindrops_search_incollection"`
		RaindropsSort               string   `json:"raindrops_sort"`
	} `json:"config"`
	Password bool `json:"password"`
}

type CollectionResponse struct {
	Result       bool       `json:"result"`
	Items        []Bookmark `json:"items"`
	Count        int        `json:"count"`
	CollectionID int        `json:"collectionId"`
}

type UserResponse struct {
	Result bool `json:"result"`
	User   User `json:"user"`
}
