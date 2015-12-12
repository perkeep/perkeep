/*
Copyright 2014 The Camlistore Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package gc

import (
	"reflect"
	"sort"
	"testing"

	"golang.org/x/net/context"
)

func sl(v ...string) []string {
	if len(v) == 0 {
		return nil
	}
	return v
}

var collectTests = []struct {
	name      string
	world     []string
	roots     []string
	graph     map[string][]string
	wantWorld []string
}{
	{
		name:      "delete everything",
		world:     sl("a", "b", "c"),
		wantWorld: sl(),
	},

	{
		name:      "keep everything",
		world:     sl("a", "b", "c"),
		roots:     sl("a", "b", "c"),
		wantWorld: sl("a", "b", "c"),
	},

	{
		name:  "keep all via chain",
		world: sl("a", "b", "c", "d", "e"),
		roots: sl("a"),
		graph: map[string][]string{
			"a": sl("b"),
			"b": sl("c"),
			"c": sl("d"),
			"d": sl("e"),
		},
		wantWorld: sl("a", "b", "c", "d", "e"),
	},

	{
		name:  "keep all via fan",
		world: sl("a", "b", "c", "d", "e"),
		roots: sl("a"),
		graph: map[string][]string{
			"a": sl("b", "c", "d", "e"),
		},
		wantWorld: sl("a", "b", "c", "d", "e"),
	},

	{
		name:  "c dies, two roots",
		world: sl("a", "b", "c", "d", "e"),
		roots: sl("a", "d"),
		graph: map[string][]string{
			"a": sl("b"),
			"d": sl("e"),
		},
		wantWorld: sl("a", "b", "d", "e"),
	},
}

type worldSet map[string]bool

func newWorldSet(start []string) worldSet {
	s := make(worldSet)
	for _, v := range start {
		s[v] = true
	}
	return s
}

func (s worldSet) Delete(it Item) error {
	delete(s, it.(string))
	return nil
}

func (s worldSet) items() []string {
	if len(s) == 0 {
		return nil
	}
	ret := make([]string, 0, len(s))
	for it := range s {
		ret = append(ret, it)
	}
	sort.Strings(ret)
	return ret
}

func TestCollector(t *testing.T) {
	for _, tt := range collectTests {
		if tt.name == "" {
			panic("no name in test")
		}
		w := newWorldSet(tt.world)
		c := &Collector{
			World:          testWorld{},
			Marker:         testMarker(map[Item]bool{}),
			Roots:          testEnum(tt.roots),
			Sweeper:        testEnum(tt.world),
			ItemEnumerator: testItemEnum(tt.graph),
			Deleter:        w,
		}
		if err := c.Collect(context.TODO()); err != nil {
			t.Errorf("%s: Collect = %v", tt.name, err)
		}
		got := w.items()
		if !reflect.DeepEqual(tt.wantWorld, got) {
			t.Errorf("%s: world = %q; want %q", tt.name, got, tt.wantWorld)
		}
	}
}

type testEnum []string

func (s testEnum) Enumerate(ctx context.Context, dest chan<- Item) error {
	defer close(dest)
	for _, v := range s {
		select {
		case dest <- v:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

type testItemEnum map[string][]string

func (m testItemEnum) EnumerateItem(ctx context.Context, it Item, dest chan<- Item) error {
	defer close(dest)
	for _, v := range m[it.(string)] {
		select {
		case dest <- v:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

type testMarker map[Item]bool

func (m testMarker) Mark(it Item) error {
	m[it] = true
	return nil
}

func (m testMarker) IsMarked(it Item) (v bool, err error) {
	v = m[it]
	return
}

type testWorld struct{}

func (testWorld) Start() error { return nil }
func (testWorld) Stop() error  { return nil }
