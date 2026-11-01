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

package access

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/tailscale/tailcat"
	"perkeep.org/pkg/health"
	"tailscale.com/types/logger"
)

// probeInterval is how often the listener checks that a client can reach
// it, through its DERP relay.
const probeInterval = 5 * time.Minute

// probeTimeout bounds each of those checks.
const probeTimeout = 30 * time.Second

// startProbesLocked starts checking, now and every probeInterval, that
// a client can reach the just-started listener.
func (h *Handler) startProbesLocked() {
	ctx, cancel := context.WithCancel(context.Background())
	h.stopProbes = cancel
	ts, addr := h.ts, h.addr
	go func() {
		for {
			err := h.probe(ctx, addr)
			if ctx.Err() != nil {
				return
			}
			h.recordProbe(ts, err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(probeInterval):
			}
		}
	}()
}

// recordProbe records the result of a self-check of the listener ts, if
// it's still the one listening.
func (h *Handler) recordProbe(ts *tailcat.Server, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ts != ts {
		return
	}
	h.probed, h.probeErr = true, err
	if err != nil {
		h.probeFails++
		log.Printf("access: tailcat self-check: %v", err)
	} else {
		h.probeFails = 0
	}
	h.updateHealthLocked()
}

// probe checks that a client can reach the listener at addr, as clients
// do, through its DERP relay: that it answers an HTTP request without an
// access key, with a 401.
func (h *Handler) probe(ctx context.Context, addr tailcat.Addr) error {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	logf := logger.Discard
	if verbose {
		logf = logger.WithPrefix(log.Printf, "tailcat self-check: ")
	}
	// With the same key each time, the listener sees the same peer.
	cl := &tailcat.Client{Server: addr, Key: h.probeKey, Logf: logf}
	defer cl.Close()
	hc := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return cl.DialTCPPort(ctx, 80)
		},
		DisableKeepAlives: true,
	}}
	req, err := http.NewRequestWithContext(ctx, "HEAD", "http://perkeep/", nil)
	if err != nil {
		return err
	}
	res, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("not reachable through its DERP relay: %w", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		return fmt.Errorf("got %v from it without an access key; want 401", res.Status)
	}
	return nil
}

// statusLocked returns the listener's status: a colored dot for it, its
// health state, and a description of it.
func (h *Handler) statusLocked() (dot string, st health.State, msg string) {
	switch {
	case !h.st.Enabled:
		return "⚪", health.StateOK, "Disabled"
	case h.ts == nil && h.startErr != nil:
		return "🔴", health.StateFailed, fmt.Sprintf("Not listening: %v; retrying", h.startErr)
	case h.ts == nil:
		return "🟡", health.StateRunning, "Starting"
	case !h.probed:
		return "🟡", health.StateRunning, "Listening, with " + h.regionLocked() + "; checking that clients can reach it"
	case h.probeErr != nil && h.probeFails >= 2:
		return "🔴", health.StateFailed, "Listening, but " + h.probeErr.Error()
	case h.probeErr != nil:
		return "🟡", health.StateWarning, "Listening, but " + h.probeErr.Error()
	}
	return "🟢", health.StateOK, "Listening, with " + h.regionLocked() + "; clients can reach it"
}

// updateHealthLocked reports the listener's status to its health check.
func (h *Handler) updateHealthLocked() {
	_, st, msg := h.statusLocked()
	switch st {
	case health.StateOK:
		h.check.OKMessage(msg)
	case health.StateRunning:
		h.check.Start()
		h.check.RunningMessage(msg)
	case health.StateWarning:
		h.check.Warn(errors.New(msg))
	default:
		h.check.Fail(errors.New(msg))
	}
}

// regionLocked describes the identity's DERP region or server.
func (h *Handler) regionLocked() string {
	id := h.st.Identity
	switch {
	case id == nil:
		return ""
	case id.DERPHost != "":
		return "DERP server " + id.DERPHost
	case h.regName != "":
		return fmt.Sprintf("tailcat DERP region %d (%s)", id.RegionID, h.regName)
	}
	return fmt.Sprintf("tailcat DERP region %d", id.RegionID)
}
