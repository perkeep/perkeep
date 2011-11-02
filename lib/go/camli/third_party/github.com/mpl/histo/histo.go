/*
Copyright Mathieu Lonjaret

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

package histo

import (
	"sort"
)

type Bar struct {
	Value  int64
	Count  int64
	Min    int64
	Max    int64
	Points []int64
}

type sortable []int64

func (s sortable) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
func (s sortable) Len() int           { return len(s) }
func (s sortable) Less(i, j int) bool { return s[i] < s[j] }

type Histo struct {
	nb       int // number of bins/bars
	np       int // number of points
	bar      [](*Bar)
	unsorted sortable // pool of points yet to be sorted
}

// NewHisto returns an histogram set up with n bins
func NewHisto(num int) *Histo {
	return &Histo{num, 0, nil, nil}
}

func (h *Histo) sort() {
	if h.unsorted != nil {
		sort.Sort(h.unsorted)
	}
}

func (h *Histo) Add(v int64) {
	h.unsorted = append(h.unsorted, v)
}

func (h *Histo) distribute() {
	if h.unsorted == nil {
		// no new points; nothing to do.
		return
	}

	h.sort()
	max := h.unsorted[len(h.unsorted)-1]
	min := h.unsorted[0]
	binWidth := 1 + (max-min)/int64(h.nb)
	np := int64(h.np)
	average := int64(0)
	sup := min + binWidth
	for _, v := range h.unsorted {
		if v > sup {
			average /= np
			h.bar = append(h.bar, &Bar{average, np, sup, sup + binWidth, nil})
			sup += binWidth
			average = v
			np = 1
		} else {
			average += v
			np++
		}
	}
	h.unsorted = nil
}

func (h *Histo) Bars() [](*Bar) {
	h.distribute()
	return h.bar
}
