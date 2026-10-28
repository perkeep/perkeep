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

package webserver

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"tailscale.com/ipn/ipnstate"
)

// TailscaleDebugHandler serves the status of the server's Tailscale node,
// as text: its peers with active WireGuard sessions, how each is reached
// (directly, through a peer relay, or through a DERP relay) and their
// traffic. With "?all=1", it lists the inactive peers too, and with
// "?json=1", it serves the node's whole status as JSON.
//
// It serves a 404 unless the server is in Tailscale tsnet mode. It doesn't
// authenticate requests; wrap it to do so.
func TailscaleDebugHandler(w http.ResponseWriter, r *http.Request) {
	ts := TSNetCtxKey.Value(r.Context())
	if ts == nil {
		http.Error(w, "not running in Tailscale mode", http.StatusNotFound)
		return
	}
	lc, err := ts.LocalClient()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	st, err := lc.Status(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if r.FormValue("json") == "1" {
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "\t")
		enc.Encode(st)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	writeTailscaleStatus(w, st, r.FormValue("all") == "1", time.Now())
}

// writeTailscaleStatus writes st to w as text, as of now, listing only
// the active peers unless all is true.
func writeTailscaleStatus(w io.Writer, st *ipnstate.Status, all bool, now time.Time) {
	fmt.Fprintf(w, "Backend state: %s\n", st.BackendState)
	if self := st.Self; self != nil {
		fmt.Fprintf(w, "Self: %s %v\n", strings.TrimSuffix(self.DNSName, "."), self.TailscaleIPs)
	}
	peers := make([]*ipnstate.PeerStatus, 0, len(st.Peer))
	inactive := 0
	for _, p := range st.Peer {
		if !p.Active && !all {
			inactive++
			continue
		}
		peers = append(peers, p)
	}
	slices.SortFunc(peers, func(a, b *ipnstate.PeerStatus) int {
		if a.Active != b.Active {
			if a.Active {
				return -1
			}
			return 1
		}
		return cmp.Compare(a.HostName, b.HostName)
	})
	fmt.Fprintf(w, "\n")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "HOST\tOS\tIP\tACTIVE\tPATH\tRX\tTX\tHANDSHAKE\tLAST WRITE\tONLINE\n")
	for _, p := range peers {
		var ip string
		if len(p.TailscaleIPs) > 0 {
			ip = p.TailscaleIPs[0].String()
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%v\t%s\t%d\t%d\t%s\t%s\t%v\n",
			p.HostName, p.OS, ip, p.Active, tailscalePeerPath(p),
			p.RxBytes, p.TxBytes, ago(now, p.LastHandshake), ago(now, p.LastWrite), p.Online)
	}
	tw.Flush()
	if inactive > 0 {
		fmt.Fprintf(w, "\n(%d inactive peers not shown; use ?all=1)\n", inactive)
	}
}

// tailscalePeerPath describes how packets to p go: directly to an address,
// through a peer relay, or through a DERP relay region.
func tailscalePeerPath(p *ipnstate.PeerStatus) string {
	switch {
	case p.CurAddr != "":
		return "direct " + p.CurAddr
	case p.PeerRelay != "":
		return "peer-relay " + p.PeerRelay
	case p.Relay != "":
		return "derp " + p.Relay
	}
	return "-"
}

// ago returns how long before now t was, or "-" if t is zero.
func ago(now, t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return now.Sub(t).Round(time.Second).String() + " ago"
}
