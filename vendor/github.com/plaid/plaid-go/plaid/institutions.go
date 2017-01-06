package plaid

// GetInstitution returns information for a single institution given an ID.
// See https://plaid.com/docs/api/#institutions-by-id.
func GetInstitution(environment environmentURL, id string) (inst institution, err error) {
	err = getAndUnmarshal(environment, "/institutions/"+id, &inst)
	return
}

// GetInstitution returns information for all institutions.
// See https://plaid.com/docs/api/#all-institutions.
func GetInstitutions(environment environmentURL) (institutions []institution, err error) {
	err = getAndUnmarshal(environment, "/institutions", &institutions)
	return
}

type institution struct {
	Credentials struct {
		Password string `json:"password"` // e.g.: "Password"
		PIN      string `json:"pin"`      // e.g.: "PIN"
		Username string `json:"username"` // e.g.: "Online ID"
	}
	Name     string   `json:"name"`     // e.g.: "Bank of America"
	HasMFA   bool     `json:"has_mfa"`  // e.g.: true
	ID       string   `json:"id"`       // e.g.: "5301a93ac140de84910000e0"
	MFA      []string `json:"mfa"`      // e.g.: ["code", "list", "questions"]
	Products []string `json:"products"` // e.g.: ["connect", "auth", "balance"]
	Type     string   `json:"type"`     // e.g.: "bofa"
}
