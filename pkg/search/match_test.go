package search

import (
	"testing"
	"time"

	"camlistore.org/pkg/types"
)

const year = time.Hour * 24 * 365

func TestTimeConstraint(t *testing.T) {
	tests := []struct {
		c    *TimeConstraint
		t    time.Time
		want bool
	}{
		{
			&TimeConstraint{
				Before: types.Time3339(time.Unix(124, 0)),
			},
			time.Unix(123, 0),
			true,
		},
		{
			&TimeConstraint{
				Before: types.Time3339(time.Unix(123, 0)),
			},
			time.Unix(123, 1),
			false,
		},
		{
			&TimeConstraint{
				After: types.Time3339(time.Unix(123, 0)),
			},
			time.Unix(123, 0),
			true,
		},
		{
			&TimeConstraint{
				After: types.Time3339(time.Unix(123, 0)),
			},
			time.Unix(123, 1),
			true,
		},
		{
			&TimeConstraint{
				After: types.Time3339(time.Unix(123, 0)),
			},
			time.Unix(122, 0),
			false,
		},
		{
			// This test will pass for 20 years at least.
			&TimeConstraint{
				InLast: 20 * year,
			},
			time.Unix(1384034605, 0),
			true,
		},
		{
			&TimeConstraint{
				InLast: 1 * year,
			},
			time.Unix(123, 0),
			false,
		},
	}
	for i, tt := range tests {
		got := tt.c.timeMatches(tt.t)
		if got != tt.want {
			t.Errorf("%d. matches(tc=%+v, t=%v) = %v; want %v", i, tt.c, tt.t, got, tt.want)
		}
	}
}
