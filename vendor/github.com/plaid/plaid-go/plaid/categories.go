package plaid

// GetCategories returns information for all categories.
// See https://plaid.com/docs/api/#category-overview.
func GetCategories(environment environmentURL) (categories []category, err error) {
	err = getAndUnmarshal(environment, "/categories", &categories)
	return
}

// GetCategory returns information for a single category given an ID.
// See https://plaid.com/docs/api/#categories-by-id.
func GetCategory(environment environmentURL, id string) (cat category, err error) {
	err = getAndUnmarshal(environment, "/categories/"+id, &cat)
	return
}

type category struct {
	Hierarchy []string `json:"hierarchy"` // e.g.: ["Food and Drink", "Bar"]
	ID        string   `json:"id"`        // e.g.: "13001000"
	Type      string   `json:"type"`      // e.g.: "place"
}
