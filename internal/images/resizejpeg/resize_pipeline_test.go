/*
Copyright 2026 The Perkeep Authors

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

package resizejpeg

import (
	"errors"
	"testing"
)

func TestResizeCollectorOutOfOrder(t *testing.T) {
	// Inject completed jobs in exactly reverse source order. This tests
	// ordering without depending on a particular goroutine schedule.
	f, err := newLanczosResizer(3, 12, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	want, err := newLanczosResizer(3, 12, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	bands := make([]*resizeBand, 4)
	for i := range bands {
		b := &resizeBand{y: i * 3, n: 3, rows: make([]float64, 3*2*3)}
		for y := 0; y < b.n; y++ {
			src := []byte{
				byte(11 * (b.y + y)), 72, 11, 255,
				19, byte(17 * (b.y + y)), 160, 255,
				210, 51, byte(13 * (b.y + y)), 255,
			}
			f.horizontal(b.rows[y*6:(y+1)*6], src)
		}
		if err := want.addRows(b.rows, b.y, b.n); err != nil {
			t.Fatal(err)
		}
		bands[i] = b
	}
	if err := want.finish(); err != nil {
		t.Fatal(err)
	}
	p := &resizePipeline{
		filter: f, bands: bands, free: make(chan *resizeBand, len(bands)),
		results: make(chan *resizeBand), errCh: make(chan error, 1), done: make(chan struct{}),
	}
	go p.collect()
	for i := len(bands) - 1; i >= 0; i-- {
		p.results <- bands[i]
	}
	close(p.results)
	<-p.done
	if p.firstErr != nil {
		t.Fatal(p.firstErr)
	}
	if len(p.free) != len(bands) {
		t.Fatalf("recycled %d bands, want %d", len(p.free), len(bands))
	}
	if err := f.finish(); err != nil {
		t.Fatal(err)
	}
	if !samePixels(f.dst, want.dst) {
		t.Fatal("completion order changed output")
	}
}

func TestResizeCollectorErrorDrains(t *testing.T) {
	f, err := newLanczosResizer(1, 3, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	workerErr := errors.New("injected worker failure")
	p := &resizePipeline{
		filter: f, free: make(chan *resizeBand, 3),
		results: make(chan *resizeBand), errCh: make(chan error, 1), done: make(chan struct{}),
	}
	go p.collect()
	// The failing later job arrives first. The collector must signal the
	// producer promptly but still drain and recycle every submitted band.
	p.results <- &resizeBand{y: 2, n: 1, rows: make([]float64, 3), err: workerErr}
	if err := <-p.errCh; !errors.Is(err, workerErr) {
		t.Fatalf("producer notification: %v, want %v", err, workerErr)
	}
	p.results <- &resizeBand{y: 0, n: 1, rows: make([]float64, 3)}
	p.results <- &resizeBand{y: 1, n: 1, rows: make([]float64, 3), err: errors.New("second failure")}
	close(p.results)
	<-p.done
	if !errors.Is(p.firstErr, workerErr) {
		t.Fatalf("returned error: %v, want first failure", p.firstErr)
	}
	if len(p.free) != 3 {
		t.Fatalf("error path recycled %d of 3 bands", len(p.free))
	}
}
