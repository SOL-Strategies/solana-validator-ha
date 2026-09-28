package consensus

import (
	"context"
	"errors"
	"time"

	"github.com/charmbracelet/log"
	"github.com/sol-strategies/solana-validator-ha/internal/config"
	"github.com/sol-strategies/solana-validator-ha/internal/logging"
	"github.com/sol-strategies/solana-validator-ha/internal/rpc"
	"github.com/solana-foundation/solana-go/v2"
)

// alpenglowFeature is the feature gate that schedules the Alpenglow migration.
var alpenglowFeature = solana.MustPublicKeyFromBase58("A1pengvuM6JEcyNuTnMqepBKhwHE3N6PmUrdATGawhJS")

// migrationDelaySlots is how many slots after the feature activates the migration starts.
// It is only used to tell operators when to expect the switch.
const migrationDelaySlots = 5000

// alpenglowReverifyInterval is how often the genesis certificate is re-read once Alpenglow is
// detected. The phase cannot change any more, so this only surfaces misbehaving RPCs in the logs.
const alpenglowReverifyInterval = 10 * time.Minute

// Reasons the local validator may not be promoted while the cluster runs Alpenglow.
const (
	ReasonLocalNotMigrated     = "local_not_migrated"
	ReasonLocalGenesisMismatch = "local_genesis_mismatch"
)

// Options configures a Detector.
type Options struct {
	Mode              config.ConsensusMode
	DetectionInterval time.Duration
	// ClusterRPC is queried for the cluster phase.
	ClusterRPC *rpc.Client
	// LocalRPC is the local validator. It is queried for its own genesis certificate, and for the
	// cluster's when no cluster RPC implements getAgGenesisCert.
	LocalRPC  *rpc.Client
	LogPrefix string
}

// Detector tracks the cluster's consensus phase and the local validator's Alpenglow genesis.
//
// The phase moves tower → migrating → alpenglow. Alpenglow is final: once a genesis certificate
// is seen, later RPC answers can no longer change the phase or the genesis slot.
// A Detector is not safe for concurrent use.
type Detector struct {
	mode              config.ConsensusMode
	detectionInterval time.Duration
	clusterRPC        *rpc.Client
	localRPC          *rpc.Client
	logger            *log.Logger
	now               func() time.Time

	phase            Phase
	genesisSlot      uint64
	clusterCheckedAt time.Time
	// localGenesisSlot is the genesis slot the local validator reports. Zero means it reported
	// none, or has not been asked yet.
	localGenesisSlot uint64
	localCheckedAt   time.Time
	warnedNoGenesis  bool
}

// NewDetector returns a Detector. A pinned mode sets the phase immediately; auto mode starts
// in PhaseUnknown until the first Refresh.
func NewDetector(opts Options) *Detector {
	d := &Detector{
		mode:              opts.Mode,
		detectionInterval: opts.DetectionInterval,
		clusterRPC:        opts.ClusterRPC,
		localRPC:          opts.LocalRPC,
		logger:            logging.New(opts.LogPrefix, "consensus"),
		now:               time.Now,
	}
	switch opts.Mode {
	case config.ConsensusModeTower:
		d.phase = PhaseTower
	case config.ConsensusModeAlpenglow:
		d.phase = PhaseAlpenglow
	}
	return d
}

// Phase returns the current consensus phase.
func (d *Detector) Phase() Phase {
	return d.phase
}

// View returns a snapshot of the phase and genesis slot.
func (d *Detector) View() View {
	return View{Phase: d.phase, GenesisSlot: d.genesisSlot}
}

// LocalEligibility reports whether the local validator may be promoted. Outside the Alpenglow
// phase it always may. In the Alpenglow phase it must report a genesis certificate, with the
// cluster's genesis slot when that is known. reason is empty when eligible.
func (d *Detector) LocalEligibility() (eligible bool, reason string) {
	if d.phase != PhaseAlpenglow {
		return true, ""
	}
	switch {
	case d.localGenesisSlot == 0:
		return false, ReasonLocalNotMigrated
	case d.genesisSlot != 0 && d.localGenesisSlot != d.genesisSlot:
		return false, ReasonLocalGenesisMismatch
	default:
		return true, ""
	}
}

// Refresh re-reads the cluster phase and the local genesis certificate when their check
// intervals have elapsed. It is cheap to call on every poll.
func (d *Detector) Refresh(ctx context.Context) {
	if d.mode == config.ConsensusModeTower {
		return
	}
	now := d.now()
	if d.clusterCheckDue(now) {
		d.refreshCluster(ctx)
		d.clusterCheckedAt = now
	}
	if d.phase == PhaseAlpenglow && d.localCheckDue(now) {
		d.refreshLocal(ctx)
		d.localCheckedAt = now
	}
}

func (d *Detector) clusterCheckDue(now time.Time) bool {
	if d.clusterCheckedAt.IsZero() {
		return true
	}
	interval := d.detectionInterval
	if d.phase == PhaseAlpenglow && d.genesisSlot != 0 {
		interval = alpenglowReverifyInterval
	}
	return now.Sub(d.clusterCheckedAt) >= interval
}

// localCheckDue re-reads a matching local certificate only at the detection interval, since it
// cannot change. Otherwise it is re-read on every call, so a node that has just migrated
// becomes eligible without waiting a full interval.
func (d *Detector) localCheckDue(now time.Time) bool {
	if eligible, _ := d.LocalEligibility(); !eligible {
		return true
	}
	return now.Sub(d.localCheckedAt) >= d.detectionInterval
}

func (d *Detector) refreshCluster(ctx context.Context) {
	genesisSlot, certErr := d.fetchGenesisSlot(ctx)
	if certErr == nil && genesisSlot != 0 {
		d.observeGenesisSlot(genesisSlot)
		return
	}
	if d.phase == PhaseAlpenglow {
		d.logMissingGenesisCert(certErr)
		return
	}

	feature, err := d.clusterRPC.GetFeatureStatus(ctx, alpenglowFeature)
	if err != nil {
		// Keep the last known phase rather than flapping on a transient RPC failure. Before the
		// first successful answer the phase stays unknown, which only trusts gossip presence.
		d.logger.Warn("failed to read the Alpenglow feature gate - keeping the current consensus phase",
			"phase", d.phase, "error", err, "genesis_cert_error", certErr)
		return
	}
	if feature.Activated {
		d.setPhase(PhaseMigrating, "feature_activated_at", feature.ActivatedAt,
			"expected_migration_slot", feature.ActivatedAt+migrationDelaySlots)
		return
	}
	d.setPhase(PhaseTower)
}

// fetchGenesisSlot returns the Alpenglow genesis slot, or zero while there is none. When no
// cluster RPC implements getAgGenesisCert, the local validator is asked instead.
func (d *Detector) fetchGenesisSlot(ctx context.Context) (uint64, error) {
	cert, err := d.clusterRPC.GetAgGenesisCert(ctx)
	if errors.Is(err, rpc.ErrMethodNotFound) {
		d.logger.Debug("no cluster RPC implements getAgGenesisCert - asking the local validator")
		cert, err = d.localRPC.GetAgGenesisCert(ctx)
	}
	if err != nil || cert == nil {
		return 0, err
	}
	return cert.Block.Slot, nil
}

func (d *Detector) observeGenesisSlot(slot uint64) {
	switch {
	case d.genesisSlot == 0:
		d.genesisSlot = slot
		d.logger.Info("Alpenglow genesis certificate found", "genesis_slot", slot)
		d.setPhase(PhaseAlpenglow)
	case slot != d.genesisSlot:
		d.logger.Error("RPC reported a different Alpenglow genesis slot - keeping the first one seen",
			"genesis_slot", d.genesisSlot, "reported_genesis_slot", slot)
	}
}

func (d *Detector) logMissingGenesisCert(err error) {
	if d.genesisSlot != 0 {
		// Lagging or older RPCs may still answer null; the phase is final regardless.
		d.logger.Debug("RPC returned no Alpenglow genesis certificate - staying in the alpenglow phase", "error", err)
		return
	}
	if !d.warnedNoGenesis {
		d.logger.Warn("cluster.consensus.mode is alpenglow but the genesis slot is unavailable - vote evidence is trusted without a warm-up", "error", err)
		d.warnedNoGenesis = true
	}
}

func (d *Detector) refreshLocal(ctx context.Context) {
	cert, err := d.localRPC.GetAgGenesisCert(ctx)
	if err != nil {
		d.logger.Debug("failed to read the local Alpenglow genesis certificate - keeping the previous result", "error", err)
		return
	}
	var slot uint64
	if cert != nil {
		slot = cert.Block.Slot
	}
	if slot != d.localGenesisSlot {
		d.logger.Info("local Alpenglow genesis slot changed",
			"local_genesis_slot", slot, "previous_local_genesis_slot", d.localGenesisSlot, "cluster_genesis_slot", d.genesisSlot)
	}
	d.localGenesisSlot = slot
}

func (d *Detector) setPhase(phase Phase, keyvals ...any) {
	if phase == d.phase {
		return
	}
	d.logger.Info("consensus phase changed", append([]any{"from", d.phase, "to", phase}, keyvals...)...)
	d.phase = phase
}
