// Package consensus works out whether the cluster runs TowerBFT or Alpenglow consensus, and
// whether the local validator has migrated to the same Alpenglow genesis as the cluster.
package consensus

// Phase is the consensus stage of the cluster as seen by this node.
type Phase int

const (
	// PhaseUnknown means no RPC has yet told us which protocol the cluster runs. It is handled
	// like PhaseMigrating: only gossip presence counts as evidence that the active peer is up.
	PhaseUnknown Phase = iota
	// PhaseTower means the cluster runs TowerBFT and the Alpenglow feature gate is not active.
	PhaseTower
	// PhaseMigrating means the Alpenglow feature gate is active but no Alpenglow genesis
	// certificate has been seen yet. Votes stop landing for everyone during this window.
	PhaseMigrating
	// PhaseAlpenglow means an Alpenglow genesis certificate has been seen. It is never left.
	PhaseAlpenglow
)

// Phases lists every phase, for exporting one metric series per phase.
var Phases = []Phase{PhaseUnknown, PhaseTower, PhaseMigrating, PhaseAlpenglow}

// String returns the lower-case phase name used in logs, metrics and recordings.
func (p Phase) String() string {
	switch p {
	case PhaseTower:
		return "tower"
	case PhaseMigrating:
		return "migrating"
	case PhaseAlpenglow:
		return "alpenglow"
	default:
		return "unknown"
	}
}

// View is a snapshot of what the detector knows, for consumers that must not query RPC themselves.
type View struct {
	Phase Phase
	// GenesisSlot is the Alpenglow genesis slot. Zero means it is not known.
	GenesisSlot uint64
}
