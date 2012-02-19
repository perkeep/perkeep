package flickr

import (
	"fmt"
	"testing"
)

func TestGetInfo(t *testing.T) {
	r := &Request{
		ApiKey: "YOURAPIKEYHERE",
		Method: "flickr.photos.getInfo",
		Args: map[string]string{
			"photo_id": "5356343650",
		},
	}

	// Don't need to sign but might as well since we're testing
	r.Sign("YOURAPISECRETHERE")

	fmt.Println(r.URL())

	resp, err := r.Execute()
	if err != nil {
		fmt.Println(err)
	}
	fmt.Println(resp)
}
