package mastodon

import (
	"encoding/json"
	"fmt"
)

type ID string

func (id *ID) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' && data[len(data)-1] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		*id = ID(s)
		return nil
	}
	var n int64
	if err := json.Unmarshal(data, &n); err != nil {
		return err
	}
	*id = ID(fmt.Sprint(n))
	return nil
}
