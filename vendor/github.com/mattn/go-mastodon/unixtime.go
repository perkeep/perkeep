package mastodon

import (
	"strconv"
	"time"
)

type Unixtime time.Time

func (t *Unixtime) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' && data[len(data)-1] == '"' {
		data = data[1 : len(data)-1]
	}
	ts, err := strconv.ParseInt(string(data), 10, 64)
	if err != nil {
		return err
	}
	*t = Unixtime(time.Unix(ts, 0))
	return nil
}
