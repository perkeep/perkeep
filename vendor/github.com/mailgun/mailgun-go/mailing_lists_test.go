package mailgun

import (
	"fmt"
	"strings"
	"testing"

	"github.com/facebookgo/ensure"
)

func setup(t *testing.T) (Mailgun, string) {
	domain := reqEnv(t, "MG_DOMAIN")
	mg, err := NewMailgunFromEnv()
	ensure.Nil(t, err)

	address := fmt.Sprintf("%s@%s", strings.ToLower(randomString(6, "list")), domain)
	_, err = mg.CreateList(List{
		Address:     address,
		Name:        address,
		Description: "TestMailingListMembers-related mailing list",
		AccessLevel: Members,
	})
	ensure.Nil(t, err)
	return mg, address
}

func teardown(t *testing.T, mg Mailgun, address string) {
	ensure.Nil(t, mg.DeleteList(address))
}

func TestMailingListMembers(t *testing.T) {
	mg, address := setup(t)
	defer teardown(t, mg, address)

	var countPeople = func() int {
		n, _, err := mg.GetMembers(DefaultLimit, DefaultSkip, All, address)
		ensure.Nil(t, err)
		return n
	}

	startCount := countPeople()
	protoJoe := Member{
		Address:    "joe@example.com",
		Name:       "Joe Example",
		Subscribed: Subscribed,
	}
	ensure.Nil(t, mg.CreateMember(true, address, protoJoe))
	newCount := countPeople()
	ensure.False(t, newCount <= startCount)

	theMember, err := mg.GetMemberByAddress("joe@example.com", address)
	ensure.Nil(t, err)
	ensure.DeepEqual(t, theMember.Address, protoJoe.Address)
	ensure.DeepEqual(t, theMember.Name, protoJoe.Name)
	ensure.DeepEqual(t, theMember.Subscribed, protoJoe.Subscribed)
	ensure.True(t, len(theMember.Vars) == 0)

	_, err = mg.UpdateMember("joe@example.com", address, Member{
		Name: "Joe Cool",
	})
	ensure.Nil(t, err)

	theMember, err = mg.GetMemberByAddress("joe@example.com", address)
	ensure.Nil(t, err)
	ensure.DeepEqual(t, theMember.Name, "Joe Cool")
	ensure.Nil(t, mg.DeleteMember("joe@example.com", address))
	ensure.DeepEqual(t, countPeople(), startCount)

	err = mg.CreateMemberList(nil, address, []interface{}{
		Member{
			Address:    "joe.user1@example.com",
			Name:       "Joe's debugging account",
			Subscribed: Unsubscribed,
		},
		Member{
			Address:    "Joe Cool <joe.user2@example.com>",
			Name:       "Joe's Cool Account",
			Subscribed: Subscribed,
		},
		Member{
			Address: "joe.user3@example.com",
			Vars: map[string]interface{}{
				"packet-email": "KW9ABC @ BOGBBS-4.#NCA.CA.USA.NOAM",
			},
		},
	})
	ensure.Nil(t, err)

	theMember, err = mg.GetMemberByAddress("joe.user2@example.com", address)
	ensure.Nil(t, err)
	ensure.DeepEqual(t, theMember.Name, "Joe's Cool Account")
	ensure.NotNil(t, theMember.Subscribed)
	ensure.True(t, *theMember.Subscribed)
}

func TestMailingLists(t *testing.T) {
	domain := reqEnv(t, "MG_DOMAIN")
	mg, err := NewMailgunFromEnv()
	ensure.Nil(t, err)

	listAddr := fmt.Sprintf("%s@%s", strings.ToLower(randomString(7, "list")), domain)
	protoList := List{
		Address:     listAddr,
		Name:        "List1",
		Description: "A list created by an acceptance test.",
		AccessLevel: Members,
	}

	var countLists = func() int {
		total, _, err := mg.GetLists(DefaultLimit, DefaultSkip, "")
		ensure.Nil(t, err)
		return total
	}

	_, err = mg.CreateList(protoList)
	ensure.Nil(t, err)
	defer func() {
		ensure.Nil(t, mg.DeleteList(listAddr))

		_, err := mg.GetListByAddress(listAddr)
		ensure.NotNil(t, err)
	}()

	actualCount := countLists()
	ensure.False(t, actualCount < 1)

	theList, err := mg.GetListByAddress(listAddr)
	ensure.Nil(t, err)

	protoList.CreatedAt = theList.CreatedAt // ignore this field when comparing.
	ensure.DeepEqual(t, theList, protoList)

	_, err = mg.UpdateList(listAddr, List{
		Description: "A list whose description changed",
	})
	ensure.Nil(t, err)

	theList, err = mg.GetListByAddress(listAddr)
	ensure.Nil(t, err)

	newList := protoList
	newList.Description = "A list whose description changed"
	ensure.DeepEqual(t, theList, newList)
}
