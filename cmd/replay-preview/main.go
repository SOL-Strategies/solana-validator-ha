// replay-preview renders the replay template with synthetic data so you can
// iterate on the template without needing real recording files.
//
// Usage:
//
//	go run ./cmd/replay-preview                    # 1 node (default)
//	go run ./cmd/replay-preview --node-count=2     # 2 nodes
//	go run ./cmd/replay-preview --node-count=3     # 3 nodes
//	go run ./cmd/replay-preview --delinquency      # delinquency bypass scenario (any node count)
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/sol-strategies/solana-validator-ha/internal/recording"
)

func main() {
	nodeCount := flag.Int("node-count", 1, "number of nodes to include in the replay (1-3)")
	delinquency := flag.Bool("delinquency", false, "simulate delinquency bypass scenario")
	flag.Parse()

	if *nodeCount < 1 || *nodeCount > 3 {
		fmt.Fprintln(os.Stderr, "error: --node-count must be 1, 2, or 3")
		os.Exit(1)
	}

	base := time.Date(2026, 3, 31, 14, 30, 10, 0, time.UTC)
	activePubkey := "ActivePubkey111111111111111111111111111111"

	type nodeSpec struct {
		name          string
		ip            string
		passivePubkey string
		file          string
		detectedAt    time.Time
		rank          int
		samples       []recording.GossipSample
		timeline      []recording.TimelineEntry
		outcome       *recording.Outcome
	}

	allNodes := []nodeSpec{
		{
			name:          "london",
			ip:            "185.26.11.91",
			passivePubkey: "LondonPassivePubkey1111111111111111111111",
			file:          "svha-ActivePubkey111-20260331T143021Z-185_26_11_91-recording.json",
			detectedAt:    base.Add(11 * time.Second),
			rank:          0,
			samples: []recording.GossipSample{
				{SampledAt: base, LeaderlessSamplesCount: 0,
					Peers: []recording.PeerSnapshot{{Name: "chicago", IP: "186.233.187.141", Role: "active"}}},
				{SampledAt: base.Add(5 * time.Second), LeaderlessSamplesCount: 1, ActivePeerDelinquent: *delinquency,
					Peers: []recording.PeerSnapshot{{Name: "chicago", IP: "186.233.187.141", Role: "missing"}}},
				{SampledAt: base.Add(10 * time.Second), LeaderlessSamplesCount: 2, ActivePeerDelinquent: *delinquency,
					Peers: []recording.PeerSnapshot{{Name: "chicago", IP: "186.233.187.141", Role: "missing"}}},
			},
			outcome: &recording.Outcome{Result: "became_active", FromNode: "chicago", ToNode: "london"},
		},
		{
			name:          "chicago",
			ip:            "186.233.187.141",
			passivePubkey: "ChicagoPassivePubkey111111111111111111111",
			file:          "svha-ActivePubkey111-20260331T143016Z-186_233_187_141-recording.json",
			detectedAt:    base.Add(11 * time.Second),
			rank:          1,
			samples: []recording.GossipSample{
				{SampledAt: base, LeaderlessSamplesCount: 0,
					Peers: []recording.PeerSnapshot{{Name: "london", IP: "185.26.11.91", Role: "passive"}}},
				{SampledAt: base.Add(5 * time.Second), LeaderlessSamplesCount: 1,
					Peers: []recording.PeerSnapshot{{Name: "london", IP: "185.26.11.91", Role: "passive"}}},
				{SampledAt: base.Add(10 * time.Second), LeaderlessSamplesCount: 2,
					Peers: []recording.PeerSnapshot{{Name: "london", IP: "185.26.11.91", Role: "passive"}}},
				{SampledAt: base.Add(16 * time.Second), LeaderlessSamplesCount: 0,
					Peers: []recording.PeerSnapshot{{Name: "london", IP: "185.26.11.91", Role: "active"}}},
			},
			timeline: []recording.TimelineEntry{
				{At: base.Add(11 * time.Second), Event: "delay_applied", Detail: "duration=5s"},
				{At: base.Add(16 * time.Second), Event: "revalidation_refresh"},
				{At: base.Add(16 * time.Second), Event: "aborted_peer_took_over", Detail: "peer=london"},
			},
			outcome: &recording.Outcome{Result: "aborted_peer_took_over", FromNode: "chicago", ToNode: "london"},
		},
		{
			name:          "frankfurt",
			ip:            "142.132.200.55",
			passivePubkey: "FrankfurtPassivePubkey11111111111111111111",
			file:          "svha-ActivePubkey111-20260331T143011Z-142_132_200_55-recording.json",
			detectedAt:    base.Add(11 * time.Second),
			rank:          2,
			samples: []recording.GossipSample{
				{SampledAt: base, LeaderlessSamplesCount: 0,
					Peers: []recording.PeerSnapshot{
						{Name: "london", IP: "185.26.11.91", Role: "passive"},
						{Name: "chicago", IP: "186.233.187.141", Role: "active"},
					}},
				{SampledAt: base.Add(5 * time.Second), LeaderlessSamplesCount: 1,
					Peers: []recording.PeerSnapshot{
						{Name: "london", IP: "185.26.11.91", Role: "passive"},
						{Name: "chicago", IP: "186.233.187.141", Role: "missing"},
					}},
				{SampledAt: base.Add(10 * time.Second), LeaderlessSamplesCount: 2,
					Peers: []recording.PeerSnapshot{
						{Name: "london", IP: "185.26.11.91", Role: "passive"},
						{Name: "chicago", IP: "186.233.187.141", Role: "missing"},
					}},
				{SampledAt: base.Add(21 * time.Second), LeaderlessSamplesCount: 0,
					Peers: []recording.PeerSnapshot{
						{Name: "london", IP: "185.26.11.91", Role: "active"},
						{Name: "chicago", IP: "186.233.187.141", Role: "missing"},
					}},
			},
			timeline: []recording.TimelineEntry{
				{At: base.Add(11 * time.Second), Event: "delay_applied", Detail: "duration=10s"},
				{At: base.Add(21 * time.Second), Event: "revalidation_refresh"},
				{At: base.Add(21 * time.Second), Event: "aborted_peer_took_over", Detail: "peer=london"},
			},
			outcome: &recording.Outcome{Result: "aborted_peer_took_over", FromNode: "chicago", ToNode: "london"},
		},
	}

	// build london's timeline (varies based on --delinquency)
	londonTimeline := []recording.TimelineEntry{}
	if *delinquency {
		londonTimeline = append(londonTimeline, recording.TimelineEntry{
			At: base.Add(11 * time.Second), Event: "delinquency_bypass_triggered",
			Detail: "peer=chicago slot_distance=87 allowed=75",
		})
	}
	londonTimeline = append(londonTimeline,
		recording.TimelineEntry{At: base.Add(11 * time.Second), Event: "rank_0_no_delay"},
		recording.TimelineEntry{At: base.Add(11 * time.Second), Event: "ensure_active_start"},
		recording.TimelineEntry{At: base.Add(11*time.Second + 310*time.Millisecond), Event: "confirmed_active", Detail: "duration=310ms"},
	)
	allNodes[0].timeline = londonTimeline

	downtime := 11*time.Second + 310*time.Millisecond
	data := &recording.ReplayData{
		ActivePubkey: activePubkey,
		FromNode:     "chicago",
		FromIP:       "186.233.187.141",
		ToNode:       "london",
		ToIP:         "185.26.11.91",
		Downtime:     &downtime,
	}

	for _, n := range allNodes[:*nodeCount] {
		data.Nodes = append(data.Nodes, recording.NodeReplaySummary{
			Info: recording.NodeInfo{
				Name:          n.name,
				IP:            n.ip,
				ActivePubkey:  activePubkey,
				PassivePubkey: n.passivePubkey,
			},
			File:       n.file,
			DetectedAt: n.detectedAt,
			Outcome:    n.outcome,
		})
		for i := range n.samples {
			s := n.samples[i]
			data.Entries = append(data.Entries, recording.MergedEntry{
				At: s.SampledAt, NodeName: n.name, NodeIP: n.ip, Kind: "sample", Sample: &s,
			})
		}
		for _, te := range n.timeline {
			data.Entries = append(data.Entries, recording.MergedEntry{
				At: te.At, NodeName: n.name, NodeIP: n.ip, Kind: "event", Event: te.Event, Detail: te.Detail,
			})
		}
	}

	sort.Slice(data.Entries, func(i, j int) bool {
		return data.Entries[i].At.Before(data.Entries[j].At)
	})

	out, err := recording.RenderReplay(data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error rendering replay: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(out)
}
