package recording

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/template"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// MergedEntry is a single item in the combined replay timeline.
type MergedEntry struct {
	At       time.Time
	NodeName string
	NodeIP   string
	// Kind is "event" or "sample"
	Kind   string
	Event  string
	Detail string
	Sample *GossipSample // non-nil when Kind == "sample"
}

// NodeReplaySummary holds per-node metadata shown in the replay header.
type NodeReplaySummary struct {
	Info       NodeInfo
	File       string
	DetectedAt time.Time
	Outcome    *Outcome
}

// ReplayData is the top-level value passed to the replay template.
type ReplayData struct {
	ActivePubkey string
	// FromNode / FromIP is the node that was active before the failover.
	FromNode string
	FromIP   string
	// ToNode / ToIP is the node that became active.
	ToNode string
	ToIP   string
	// Downtime is the duration between the last observed active peer and the
	// confirmed_active event (or first post-streak leaderless=0 sample).
	// Nil when the timeline does not contain enough data to compute it.
	Downtime *time.Duration
	Nodes    []NodeReplaySummary
	Entries  []MergedEntry
}

// LoadReplayData reads one or more recording files and merges them into a ReplayData.
// Files from the same cluster are identified by a shared ActivePubkey; a warning is
// returned (non-fatal) when files carry different active pubkeys.
func LoadReplayData(paths []string) (*ReplayData, []string, error) {
	if len(paths) == 0 {
		return nil, nil, fmt.Errorf("no recording files provided")
	}

	var warnings []string
	var nodes []NodeReplaySummary
	var entries []MergedEntry
	var activePubkey string

	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, nil, fmt.Errorf("reading %s: %w", p, err)
		}
		var ev FailoverEvent
		if err := json.Unmarshal(raw, &ev); err != nil {
			return nil, nil, fmt.Errorf("parsing %s: %w", p, err)
		}

		if activePubkey == "" {
			activePubkey = ev.Node.ActivePubkey
		} else if ev.Node.ActivePubkey != activePubkey {
			warnings = append(warnings, fmt.Sprintf(
				"file %s has active_pubkey %s but expected %s — files may be from different clusters",
				p, ev.Node.ActivePubkey, activePubkey,
			))
		}

		nodes = append(nodes, NodeReplaySummary{
			Info:       ev.Node,
			File:       p,
			DetectedAt: ev.DetectedAt,
			Outcome:    ev.Outcome,
		})

		for i := range ev.GossipSamples {
			s := ev.GossipSamples[i]
			entries = append(entries, MergedEntry{
				At:       s.SampledAt,
				NodeName: ev.Node.Name,
				NodeIP:   ev.Node.IP,
				Kind:     "sample",
				Sample:   &s,
			})
		}
		for _, te := range ev.Timeline {
			entries = append(entries, MergedEntry{
				At:       te.At,
				NodeName: ev.Node.Name,
				NodeIP:   ev.Node.IP,
				Kind:     "event",
				Event:    te.Event,
				Detail:   te.Detail,
			})
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].At.Equal(entries[j].At) {
			// stable secondary sort: events before samples, then by node name
			if entries[i].Kind != entries[j].Kind {
				return entries[i].Kind == "event"
			}
			return entries[i].NodeName < entries[j].NodeName
		}
		return entries[i].At.Before(entries[j].At)
	})

	// build IP lookup: prefer node info (authoritative), fall back to peer snapshots
	ipByName := map[string]string{}
	for _, n := range nodes {
		ipByName[n.Info.Name] = n.Info.IP
	}
	for _, e := range entries {
		if e.Kind == "sample" && e.Sample != nil {
			for _, p := range e.Sample.Peers {
				if _, ok := ipByName[p.Name]; !ok && p.IP != "" {
					ipByName[p.Name] = p.IP
				}
			}
		}
	}

	// resolve from/to from the first outcome that has non-unknown values
	var fromNode, toNode string
	for _, n := range nodes {
		if n.Outcome == nil {
			continue
		}
		if fromNode == "" && n.Outcome.FromNode != "unknown" {
			fromNode = n.Outcome.FromNode
		}
		if toNode == "" && n.Outcome.ToNode != "unknown" {
			toNode = n.Outcome.ToNode
		}
		if fromNode != "" && toNode != "" {
			break
		}
	}

	// compute downtime: last leaderless=0 sample before the streak → confirmed_active
	// event, falling back to the first leaderless=0 sample after the streak.
	var downtimeStart, downtimeEnd time.Time
	inStreak := false
	for _, e := range entries {
		if e.Kind == "sample" {
			if e.Sample.LeaderlessSamplesCount == 0 {
				if !inStreak {
					downtimeStart = e.At // keep updating until streak begins
				} else if downtimeEnd.IsZero() {
					downtimeEnd = e.At // first recovery sample after streak
				}
			} else {
				inStreak = true
			}
		}
		if e.Kind == "event" && e.Event == "confirmed_active" {
			downtimeEnd = e.At // precise end — prefer over sample-based estimate
		}
	}
	var downtime *time.Duration
	if !downtimeStart.IsZero() && !downtimeEnd.IsZero() {
		d := downtimeEnd.Sub(downtimeStart).Round(time.Millisecond)
		downtime = &d
	}

	return &ReplayData{
		ActivePubkey: activePubkey,
		FromNode:     fromNode,
		FromIP:       ipByName[fromNode],
		ToNode:       toNode,
		ToIP:         ipByName[toNode],
		Downtime:     downtime,
		Nodes:        nodes,
		Entries:      entries,
	}, warnings, nil
}

// RenderReplay renders the merged replay to a string using a Go template.
func RenderReplay(data *ReplayData) (string, error) {
	// assign a distinct color to each node so events are easy to tell apart.
	// #00B894 (active green) and #FF6B6B (passive red) are intentionally excluded
	// — those are reserved for active/passive role indicators in the timeline.
	palette := []lipgloss.Color{
		lipgloss.Color("#00BFFF"), // blue
		lipgloss.Color("#F9CA24"), // yellow
		lipgloss.Color("#A29BFE"), // lavender
		lipgloss.Color("#FD79A8"), // pink
		lipgloss.Color("#FDCB6E"), // amber
	}
	nodeColor := map[string]lipgloss.Color{}
	for i, n := range data.Nodes {
		nodeColor[n.Info.Name] = palette[i%len(palette)]
	}

	// pad node names to the same width so timeline columns align
	maxNodeNameLen := 0
	for _, n := range data.Nodes {
		if len(n.Info.Name) > maxNodeNameLen {
			maxNodeNameLen = len(n.Info.Name)
		}
	}
	nodeFmt := fmt.Sprintf("%%-%ds", maxNodeNameLen)
	colorNode := func(name string) string {
		c, ok := nodeColor[name]
		if !ok {
			return fmt.Sprintf(nodeFmt, name)
		}
		return lipgloss.NewStyle().Foreground(c).Bold(true).Render(fmt.Sprintf(nodeFmt, name))
	}
	muted := func(s string) string {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Render(s)
	}
	lightGrey := func(s string) string {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#999999")).Render(s)
	}
	purple := func(s string) string {
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("99")).Render(s)
	}
	hrule := func() string {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#666666")).Render(strings.Repeat("─", 80))
	}
	outcomeStyle := func(result string) string {
		switch {
		case result == "became_active":
			return lipgloss.NewStyle().Foreground(lipgloss.Color("#00B894")).Bold(true).Render(result)
		case strings.HasPrefix(result, "aborted"):
			return lipgloss.NewStyle().Foreground(lipgloss.Color("#FF6B6B")).Render(result)
		default:
			return lipgloss.NewStyle().Foreground(lipgloss.Color("#999999")).Render(result)
		}
	}
	truncPubkey := func(s string) string {
		if len(s) <= 16 {
			return s
		}
		return s[:8] + "..." + s[len(s)-4:]
	}
	formatTime := func(t time.Time) string {
		return t.UTC().Format("15:04:05.000Z")
	}
	formatPeers := func(peers []PeerSnapshot) string {
		parts := make([]string, 0, len(peers))
		for _, p := range peers {
			var roleStyled string
			switch p.Role {
			case "active":
				roleStyled = lipgloss.NewStyle().Foreground(lipgloss.Color("#00B894")).Render("active")
			case "passive":
				roleStyled = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF6B6B")).Render("passive")
			default:
				roleStyled = lipgloss.NewStyle().Foreground(lipgloss.Color("#666666")).Render("missing")
			}
			parts = append(parts, p.Name+":"+roleStyled)
		}
		return "[" + strings.Join(parts, "  ") + "]"
	}

	funcMap := template.FuncMap{
		"Node":        colorNode,
		"Muted":       muted,
		"LightGrey":   lightGrey,
		"Purple":      purple,
		"HRule":       hrule,
		"Outcome":     outcomeStyle,
		"TruncPubkey": truncPubkey,
		"FormatTime":  formatTime,
		"FormatPeers": formatPeers,
		"IsEvent":     func(e MergedEntry) bool { return e.Kind == "event" },
		"IsSample":    func(e MergedEntry) bool { return e.Kind == "sample" },
		"FormatDuration": func(d *time.Duration) string {
			if d == nil {
				return "unknown"
			}
			return d.String()
		},
	}

	tpl, err := template.New("replay").Funcs(funcMap).Parse(replayTemplate)
	if err != nil {
		return "", fmt.Errorf("parsing replay template: %w", err)
	}

	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("executing replay template: %w", err)
	}
	return buf.String(), nil
}

const replayTemplate = `{{ HRule }}
  {{ Purple "solana-validator-ha failover replay" }}
  {{ Muted "identity:" }} {{ LightGrey .ActivePubkey }}
  {{ Muted "failover:" }} {{ LightGrey (printf "%s (%s) → %s (%s)" .FromNode .FromIP .ToNode .ToIP) }}
  {{ Muted "downtime:" }} {{ LightGrey (printf "~%s" (FormatDuration .Downtime)) }}
  {{ Muted "files:" }}
{{ range .Nodes }}    {{ Muted "-" }} {{ LightGrey .File }}
{{ end }}{{ HRule }}
{{ range .Entries }}{{ if IsSample . }}  {{ LightGrey (FormatTime .At) }}  {{ Node .NodeName }}  {{ Muted "gossip" }}  leaderless={{ .Sample.LeaderlessSamplesCount }}{{ if .Sample.ActivePeerDelinquent }}  {{ Muted "delinquent=true" }}{{ end }}{{ if .Sample.RPCError }}  {{ Muted "rpc_error=true" }}{{ end }}  {{ FormatPeers .Sample.Peers }}
{{ else }}  {{ LightGrey (FormatTime .At) }}  {{ Node .NodeName }}  {{ Muted .Event }}{{ if .Detail }}  {{ LightGrey .Detail }}{{ end }}
{{ end }}{{ end }}{{ HRule }}
`
