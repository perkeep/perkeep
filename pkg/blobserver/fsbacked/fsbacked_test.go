package fsbacked

import (
	"fmt"
	"testing"
)

func TestFindRelPath(t *testing.T) {
	cases := []struct {
		root, path, want string
	}{
		{"a/b", "a/b/c", "c"},
		{"a/b", "a/b/c/d", "c/d"},
		{"a/b", "a/b", ""},
		{"a/b", "a/c", ""},
		{"a/b", "a", ""},
		{"a/b", "c/d", ""},
	}

	for i, c := range cases {
		t.Run(fmt.Sprintf("case_%02d", i+1), func(t *testing.T) {
			s := &Storage{root: c.root}
			got := s.findRelPath(c.path)
			if got != c.want {
				t.Errorf("got %s, want %s", got, c.want)
			}
		})
	}
}
