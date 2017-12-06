package mailgun

import (
	"testing"

	"github.com/facebookgo/ensure"
)

func TestRouteCRUD(t *testing.T) {
	mg, err := NewMailgunFromEnv()
	ensure.Nil(t, err)

	var countRoutes = func() int {
		count, _, err := mg.GetRoutes(DefaultLimit, DefaultSkip)
		ensure.Nil(t, err)
		return count
	}

	routeCount := countRoutes()

	newRoute, err := mg.CreateRoute(Route{
		Priority:    1,
		Description: "Sample Route",
		Expression:  "match_recipient(\".*@samples.mailgun.org\")",
		Actions: []string{
			"forward(\"http://example.com/messages/\")",
			"stop()",
		},
	})
	ensure.Nil(t, err)
	ensure.True(t, newRoute.ID != "")

	defer func() {
		ensure.Nil(t, mg.DeleteRoute(newRoute.ID))
		_, err = mg.GetRouteByID(newRoute.ID)
		ensure.NotNil(t, err)
	}()

	newCount := countRoutes()
	ensure.False(t, newCount <= routeCount)

	theRoute, err := mg.GetRouteByID(newRoute.ID)
	ensure.Nil(t, err)
	ensure.DeepEqual(t, newRoute, theRoute)

	changedRoute, err := mg.UpdateRoute(newRoute.ID, Route{
		Priority: 2,
	})
	ensure.Nil(t, err)
	ensure.DeepEqual(t, changedRoute.Priority, 2)
	ensure.DeepEqual(t, len(changedRoute.Actions), 2)
}
