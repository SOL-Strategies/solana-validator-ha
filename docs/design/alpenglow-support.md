# Design: Alpenglow-aware failover

Status: **proposed** · Verified against agave `v4.3.0-rc.1` (`2e10d67f90`) · A reference
implementation has been tested on testnet (§11)

## 1. Problem

`solana-validator-ha` decides that the active peer is down when, for `leaderless_samples_threshold`
consecutive samples, the active identity is either missing from gossip or not voting. "Not voting"
means it appears in `getVoteAccounts.delinquent`, or has no vote account at all.

After the Alpenglow migration, the gossip signal is unchanged, but the voting signal changes meaning:

| Signal | TowerBFT | Alpenglow |
|---|---|---|
| `lastVote` updated by | vote transactions landing | block-footer processing only: reward cert for slot S−8 in block S, or signer of an included finalization cert (`runtime/src/block_component_processor/vote_reward.rs`) |
| Healthy `slot − lastVote` | ~1–3 | ~1–9 (8-slot reward offset + skipped leaders) |
| `getHealth` compares against | latest optimistic slot seen via gossip | Votor's highest **finalized** slot. Returns `Unknown` until one has been observed (`rpc/src/rpc_health.rs`) |
| What stops voting that failover can't fix | identity balance below rent-exempt (vote tx fees) | vote account fails VAT (no BLS key, balance < VAT minimum) or falls outside top `MAX_ALPENGLOW_VOTE_ACCOUNTS` (`bank.rs get_vat_health_for_next_epoch`) |
| Cluster-wide stall | rare | Votor "standstill" (`DELTA_STANDSTILL` = 10s without finalization): **every** validator's `lastVote` freezes |

Consequences for the current code:

1. The 128-slot delinquency check is a slow proxy (~51s + samples ≈ 66s to fail over a zombie node).
2. `delinquency_bypass` and a low `delinquent_slot_distance_override` would fail over during a
   cluster-wide stall, where failover cannot help and adds equivocation risk.
3. The identity low-balance exemption is obsolete. Alpenglow votes are BLS messages, not transactions.
4. **Latent bug:** a VAT-excluded vote account likely disappears from `getVoteAccounts`. Today that
   path logs "no current or delinquent vote account found" and counts a leaderless sample, so peers
   would fail over to each other in a loop, and none of them can vote.
5. Around the migration, TowerBFT votes stop and certs only cover slots after the Alpenglow genesis
   slot `G`, so `lastVote` stalls for everyone for a while.
6. solana-go v1.8.4 decodes `epochCredits` as `int64`. Alpenglow puts `u64::MAX` into the epoch
   credits of some delinquent vote accounts, which fails the whole `getVoteAccounts` call. This is
   fixed by #62 (solana-go v2), which everything below builds on.

## 2. Goals / non-goals

**Goals**
- Detect the consensus phase (tower / migrating / alpenglow) automatically from RPC, with a manual override.
- Keep TowerBFT failover decisions unchanged while the cluster is on tower.
- On Alpenglow: detect a zombie active (in gossip, votes not landing) in ~25–30s, but never fail over
  on vote-based evidence while the cluster itself is not finalizing.
- Never fail over for conditions a failover can't fix (VAT exclusion).
- Never promote a local node that hasn't migrated or is on a different Alpenglow genesis.
- Full observability: metrics, logs, recording fields, replay.

**Non-goals**
- Per-slot reward-cert inclusion tracking. That needs an agave patch or Geyser footer decoding. It
  can be added later as an optional external signal.
- Syncing vote-history files on failover. That is the job of the user-supplied active command,
  documented in the README only.
- Changing the gossip-absence trigger.

## 3. Detecting the consensus phase

### 3.1 RPC signals

| Source | Call | Meaning |
|---|---|---|
| Feature gate `A1pengvuM6JEcyNuTnMqepBKhwHE3N6PmUrdATGawhJS` | `getAccountInfo` (base64). Data is bincode `Feature { activated_at: Option<u64> }`, i.e. 9 bytes `[1, u64 LE]` when active | Migration scheduled: `migration_slot = activated_at + 5000` |
| `getAgGenesisCert` (BankData RPC, finalized bank) | `{"method":"getAgGenesisCert"}` | `null` while still on tower or migrating; `{"block":{"slot":G,...}}` once Alpenglow is enabled. Blocks after `G` are Alpenglow |

`getAgGenesisCert` is new. RPCs that don't implement it answer JSON-RPC `-32601 Method not found`.

### 3.2 Phase state machine

```
            feature active            genesis cert seen
  TOWER ─────────────────▶ MIGRATING ─────────────────▶ ALPENGLOW (sticky)
    ▲                          │
    └── feature not active ────┘   (only possible before cert; e.g. RPC flapping)

  UNKNOWN: no successful answer yet → behaves like MIGRATING (conservative)
```

Rules:
- **ALPENGLOW is sticky.** Once a genesis cert with slot `G` is observed, the detector never leaves
  ALPENGLOW for the rest of the process lifetime. A lagging or old RPC returning `null` is logged at
  debug level and ignored. A cert with a *different* `G` is logged as an error and ignored, because
  the first value wins.
- **Unsupported RPCs.** Every cluster URL is asked for `getAgGenesisCert` on each detection cycle.
  If all of them answer `-32601`, the local validator is asked instead. Nothing is remembered as
  "unsupported": the extra call is cheap, and RPCs or a local validator upgraded while the process
  runs are picked up without a restart.
- **Transient errors keep the last known phase.** If the feature-gate query fails, the phase does not
  change, so an RPC blip cannot flap a TowerBFT cluster to "gossip only". The phase is UNKNOWN only
  until the first successful answer.
- **Poll cadence:** every `cluster.consensus.detection_interval_duration` (default 60s) while not yet
  ALPENGLOW. Once ALPENGLOW, re-verify every 10 min for logging only.
- `cluster.consensus.mode: tower|alpenglow` pins the phase and skips detection. In pinned `alpenglow`
  mode, `G` is still fetched (retried at the detection interval until found), because the warm-up
  guard needs it. Until it is known, warm-up is skipped with a warning.

### 3.3 Local-node check
The detector also calls `getAgGenesisCert` on `validator.rpc_url`. When the cluster phase is
ALPENGLOW, the local node is **ineligible to be promoted** unless its cert exists and its slot equals
the cluster's `G`. This check comes on top of `getHealth`, which returns `Unknown` until Votor sees
a finalization. Reasons: `local_not_migrated` or `local_genesis_mismatch`.

The local cert is re-read on every poll while the node is ineligible, so a node that has just
migrated becomes eligible without waiting a detection interval, and every detection interval once
it matches. A local RPC error keeps the previous result.

## 4. Failover logic per phase

```
failover when, for N consecutive samples:
    active_down  AND  self_eligible  AND  no veto
```

| Phase | `active_down` | Vote evidence | Vetoes added | `delinquency_bypass` |
|---|---|---|---|---|
| TOWER | gossip-absent **or** delinquent/no vote account | as today (128 slots / override, identity balance exemption) | none | as configured |
| UNKNOWN / MIGRATING | gossip-absent only | ignored. The active in gossip counts as active | none | ignored (warn once) |
| ALPENGLOW, warm-up (`finalized ≤ G + warmup_slots`) | gossip-absent only | ignored | none | ignored |
| ALPENGLOW, steady | gossip-absent **or** `lag > vote_lag_slots_threshold` while cluster live | lag-based (below) | stall, VAT/excluded | ignored (superseded) |

In the ALPENGLOW phase, every takeover also requires the local-genesis check (§3.3), including
gossip-absent ones: it is a property of this node, like health.

### 4.1 Alpenglow vote evaluation (replaces `isNodeActiveAndVoting` in ALPENGLOW phase)

```
verdict evaluateAlpenglowVoting(node):
    r, err := clusterRPC.GetVoteLag(votePubkey)   // getVoteAccounts{keepUnstakedDelinquents:true} + getSlot(processed), SAME URL
    if err                         → VOTING (assume innocence, as today)
    if account not found / 0 stake → VOTING, veto=vote_account_excluded   // failover can't fix
    lag := r.processedSlot - r.lastVote
    if lag <= L                    → VOTING
    if !clusterLive                → VOTING, veto=cluster_stalled   // everyone is frozen
    if processedSlot - finalized > L → VOTING, veto=cluster_stalled // finalization alone explains the lag
    if stakeGateEnabled && !(ratio fresh && ratio ≥ min) → VOTING, veto=cluster_stalled
    else                           → NOT_VOTING (reason=vote_lag, lag, L)
```

- **Same-URL pinning.** `lastVote` and the reference slot must come from the same RPC. Otherwise
  URL rotation mixes banks from different nodes and the lag is meaningless. `rpc.Client.GetVoteLag`
  runs both calls inside one `executeWithRetry` operation, so both calls use one `*rpc.Client`.
- **`clusterLive`.** Once per sample, call `getSlot(finalized)` (cheap). Keep a monotonic max, since
  different URLs may be slightly behind. Live means the max advanced within
  `finalization_stall_duration` (default 15s, which is > `DELTA_STANDSTILL` 10s plus a poll interval).
  The first observation is only a baseline: the cluster counts as live once an advance is seen.
- **Finalization lag.** Last votes freeze the moment finalization stops, but the time-based check only
  reacts after `finalization_stall_duration`, which leaves room for up to ~2 vote-lag samples. If
  finalization trails the processed slot by more than `L`, the cluster counts as stalled at once.
- **Stake gate (optional, default on).** Every `network_stake_check_interval_duration` (default 60s),
  make one unfiltered `getVoteAccounts`. Compute `ratio = Σcurrent.activatedStake / Σ(current+delinquent)`.
  The value is stale after 3 intervals, and a stale ratio closes the gate. `network_current_stake_ratio_min: 0`
  disables the gate.
- The identity-balance lookup is skipped in ALPENGLOW.
- A vote-lag sample reuses the delinquency slot evidence in recordings: `LastVoteSlot`,
  `CurrentSlot`, `SlotDistance`.

**Why this is sound.** A finalization certificate needs ≥60% of stake. If the cluster keeps
finalizing and the active's `lastVote` doesn't advance, its votes are not reaching leaders or
certs, so the fault is local to the active node.

### 4.2 Where vetoes act

A vetoed sample counts the active as present, so the leaderless streak resets and the veto usually
settles the matter at sample level. The manager re-checks the stall and exclusion vetoes once more
after the rank delay, because the cluster may stall while a lower-ranked node waits. A streak in
which the active was ever missing from gossip is never vetoed: a dead host is dead during a stall too.

| Veto | Outcome result | Timeline event |
|---|---|---|
| cluster stalled (no finalization / finalization lag / low stake ratio) | `aborted_cluster_stalled` | `veto_cluster_stalled` |
| active vote account excluded | `aborted_vote_account_excluded` | `veto_vote_account_excluded` |
| local not migrated / genesis mismatch (eligibility) | `aborted_local_not_migrated` | `veto_local_genesis` |

To tell the evidence types apart, `gossip.State` tracks per-sample `LeaderlessReason`
(`gossip_absent` | `vote_lag` | `no_vote_account` | `delinquent`).
`failover_vetoes_total{reason}` counts vetoed samples plus aborted takeovers.

### 4.3 Timing (defaults)
- Zombie active: lag reaches 32 slots (≈12.8s at 400ms), then 3 samples (≈11–15s) ≈ **25–30s**. Today it is ≈66s.
- Host down: unchanged, ≈11–15s.
- Thresholds are in **slots**, not seconds. This agave tree supports per-slot durations (`ns_per_slot_at_slot`).

## 5. Network isolation of the active

An active node that loses the network cannot see gossip, so it never counts leaderless samples: a
failed cluster RPC call is not a sample. It stays staked until the network returns and then runs
alongside the peer that replaced it, until Agave's duplicate-instance check shuts one of them down.
The testnet run in §11 confirmed this.

From the node's side, "I lost the network" and "my RPC provider is down" look the same. Only the
isolated node also stops receiving blocks, so an **active** node demotes itself when both hold:

1. the cluster RPC has failed for `leaderless_samples_threshold` consecutive polls, and
2. its local processed slot, read from `validator.rpc_url` over loopback, has not moved for
   `failover.isolation.local_slot_stall_duration` (default 15s).

A cluster RPC outage with the local slot still moving changes nothing, so a provider outage cannot
take a healthy active offline, including the last node standing. An unreadable local slot never
triggers demotion. Local `/health` does not help: on testnet it stayed `ok` for a node that was fully
cut off.

The local gossip peer count was considered as the local signal and rejected: in agave, gossip entries
for staked nodes survive 2 days without contact (`CRDS_GOSSIP_PURGE_DURATION`) and unstaked ones only
15s, so the count barely drops on an isolated node.

A related bug: `IsSelfPassive()` treats an unreachable local RPC as "not passive", so demoting a
stopped validator was recorded as `demotion_failed` and re-run every poll. The passive command now
reports `demoted_validator_down` in that case and is not re-run until the local RPC answers again.

## 6. Configuration

```yaml
cluster:
  consensus:
    # auto | tower | alpenglow. Default auto.
    mode: auto
    # How often to query the consensus phase before Alpenglow is detected. Default 60s.
    detection_interval_duration: 60s

failover:
  alpenglow:
    # Slots of (processed_slot - lastVote) above which the active is considered not voting. Default 32, min 16.
    vote_lag_slots_threshold: 32
    # Slots after the Alpenglow genesis slot G before vote evidence is trusted. Default 64.
    warmup_slots: 64
    # The cluster counts as stalled if the finalized slot hasn't advanced for this long.
    # Default 15s, or poll_interval_duration if longer.
    finalization_stall_duration: 15s
    # Minimum share of stake that must be current for vote evidence to count. 0 disables. Default 0.85.
    network_current_stake_ratio_min: 0.85
    # How often to compute the ratio above (unfiltered getVoteAccounts). Default 60s.
    network_stake_check_interval_duration: 60s

  isolation:
    # Demote an active node that has lost the network (§5). Default true.
    enabled: true
    # How long the local processed slot may stand still. Default 15s, or poll_interval_duration if longer.
    local_slot_stall_duration: 15s
```

Validation:
- `vote_lag_slots_threshold ≥ 16`.
- `finalization_stall_duration ≥ poll_interval_duration`, and the same for `local_slot_stall_duration`.
  Both default to at least the poll interval, so existing configs with a slow poll stay valid.
- The ratio must be in `[0,1]`.
- Startup warnings:
  - `mode: alpenglow` together with `delinquency_bypass: true`: the bypass is ignored.
  - `delinquent_slot_distance_override` is set and mode isn't `tower`: the override applies only in the tower phase.

## 7. Observability

**Metrics** (new, all with the common labels):

| Metric | Type | Notes |
|---|---|---|
| `solana_validator_ha_consensus_phase{phase}` | gauge | 1 for the current phase |
| `solana_validator_ha_alpenglow_genesis_slot` | gauge | `G`, 0 if unknown |
| `solana_validator_ha_active_vote_lag_slots` | gauge | last measured lag, removed when not measured |
| `solana_validator_ha_failover_vetoes_total{reason}` | counter | |
| `solana_validator_ha_local_alpenglow_genesis_match` | gauge | 1/0, Alpenglow phase only |
| `solana_validator_ha_cluster_finalized_slot` | gauge | monotonic max, Alpenglow phase only |
| `solana_validator_ha_cluster_live` | gauge | 1/0, Alpenglow phase only |
| `solana_validator_ha_network_current_stake_ratio` | gauge | Alpenglow phase only |

Values go through `cache.State`, like the existing metrics.

**Recordings: schema v3**
- `ConfigSnapshot` adds: `consensus_mode`, `vote_lag_slots_threshold`, `warmup_slots`,
  `finalization_stall_duration`, `network_current_stake_ratio_min`.
- `GossipSample` adds: `consensus_phase`, `alpenglow_genesis_slot`, `local_genesis_match`,
  `finalized_slot`, `cluster_live`, `network_stake_ratio`, `leaderless_reason`, `veto`.
- Timeline events added: `consensus_phase_changed`, `veto_*`, `alpenglow_vote_lag_exceeded`,
  `isolation_detected`.
- Outcomes added: `aborted_cluster_stalled`, `aborted_vote_account_excluded`,
  `aborted_local_not_migrated`, `demoted_validator_down`.
- Replay accepts v1–v3 and shows the phase, leaderless reason, liveness and veto when present.
  Golden scenarios `alpenglow-zombie` and `alpenglow-stall`.

## 8. Code changes

| Area | File(s) | Change |
|---|---|---|
| RPC | `internal/rpc/clients.go` | `GetAgGenesisCert` (raw `RPCCallForInto`, typed result, `ErrMethodNotFound` detection), `GetFeatureStatus`, `GetVoteLag` (pinned URL), `GetSlotWithCommitment` |
| Consensus | **new** `internal/consensus/{detector,phase}.go` + tests | Phase enum, sticky state machine, local-genesis check |
| Config | `internal/config/{cluster,failover,config}.go` + **new** `alpenglow.go`, `isolation.go` | Structs, defaults, validation, warnings |
| Gossip state | `internal/gossip/state.go` + **new** `alpenglow.go` | `SetConsensusView`; keep `isNodeActiveAndVoting` for tower; Alpenglow vote evaluation, finalized-slot tracker, stake-ratio cache, `LeaderlessReason`, `VetoReason` |
| Manager | `internal/ha/manager.go` + **new** `isolation.go` | Share one cluster RPC client; refresh the detector each poll; vetoes + local-genesis eligibility; isolation demotion; demotion outcomes |
| Metrics / cache | `internal/prometheus/metrics.go`, `internal/cache/cache.go` | New gauges and counters |
| Recording | `internal/recording/{event,replay}.go` | Schema v3 + replay rendering + goldens |
| Mock RPC | `integration/mock-solana/main.go` | Advancing slot clock; `getAgGenesisCert`, `getAccountInfo` (feature), `getSlot` by commitment, per-validator lag; control endpoints `set_phase`, `set_vote_lag`, `stall_finalization`, `resume_finalization`, `set_local_genesis`, `exclude_vote_account` |
| Orchestrator | `integration/test-orchestrator/main.go` | New actions matching the endpoints above, plus `assert_metric` |
| Docs | `README.md` | "Alpenglow" and "Network isolation" sections; fix the "thresholds agree" claim; vote-history note for active/passive scripts |

## 9. Test plan

**Unit**
- Detector: feature-account decoding (inactive/active/malformed); `-32601` fallback; sticky ALPENGLOW;
  conflicting `G`; pinned modes; local genesis mismatch.
- Alpenglow vote evaluation, table tests: lag ≤ L, > L live, > L stalled, finalization lag, stake gate,
  rpc error, excluded, warm-up.
- Manager: vetoes apply only to vote-based streaks; gossip-absent failover still fires during a
  stall; tower phase unchanged (existing manager tests pinned to tower); isolation demotion and the
  provider-outage case; demotion outcomes.
- Config validation and defaults.
- Replay golden files for v3.

**Integration scenarios** (`integration/scenarios/`, run in file order after 01–04)
- `05-migration-window`: phase=migrating, lag huge → no failover; disconnect → failover. Runs first,
  because HA clients never leave the alpenglow phase.
- `06-alpenglow-zombie-active`: active stays in gossip, lag rises past L → one passive takes over.
- `07-alpenglow-cluster-stall`: finalization stalls + lag rises → **no** failover; resume → no failover.
- `08-alpenglow-stall-plus-host-down`: stall + active disconnects from gossip → failover (gossip path).
- `09-local-not-migrated`: the rank-0 passive has no local genesis → rank-1 takes over.
- `10-vote-account-excluded`: active's vote account missing → no failover, veto metric increments.
- `11-phase-sticky`: after alpenglow, the mock answers as TowerBFT → phase stays alpenglow.
- Existing scenarios 01–04 run unchanged with `mode: auto` on a tower mock.

## 10. Delivery plan

A stack of PRs, each building and passing tests on its own. Only PR 5 and the two fixes change
failover behaviour.

| # | PR | Behaviour change |
|---|---|---|
| 1 | solana-go v2 (#62) + gofmt and test-stub follow-up | fixes the `epochCredits` overflow |
| 2 | Consensus phase detection: RPC methods, `internal/consensus`, `cluster.consensus` | none |
| 3 | Alpenglow vote evidence in `gossip.State`, `failover.alpenglow` | none (the gossip state defaults to tower) |
| 4 | Recording schema v3 + replay | none |
| 5 | Manager wiring: detector, vetoes, eligibility, metrics | **yes** |
| 6 | Mock-solana simulation + integration scenarios 05–11 | test only |
| 7 | README + this document | none |
| 8 | `demoted_validator_down` (§5) | yes (recording and log only) |
| 9 | Network isolation of the active (§5) | yes |

## 11. Testnet results

The reference implementation (PRs 1–7, before the §5 fixes) ran live on a two-node testnet pair,
with testnet on Alpenglow. Settings: poll 5s, `leaderless_samples_threshold` 3, rank delay 5s. The
validator unit always starts passive (an `ExecStartPre` points the identity at the unstaked key), so
any restart hands the stake to the peer.

| Case | Result | Detect → promote |
|---|---|---|
| Stop the active validator | pass | 22.2s |
| `kill -9` the active validator | pass | 28.1s (incl. 5s rank delay) |
| `kill -9` the active, fail back the other way | pass | 21.4s |
| `kill -9` the passive validator | pass, no action | — (max vote lag 8 slots) |
| Stop the HA daemon for 60s on each node | pass, no action | — (max vote lag 6 slots) |
| Egress-only block on the active's validator | **fail**, split-brain | 26s, then manual recovery |
| Full in+out network outage of the active | pass | 18.8s |

Observations:
- Both HA daemons ran in the alpenglow phase throughout, with vote evidence active.
- Healthy Alpenglow vote lag stayed at 0–8 slots, well below the default threshold of 32.
- In the full outage, the isolated active never demoted itself and stayed staked for 2m22s. §5 now
  handles this. Local `/health` stayed `ok` throughout.
- Demoting a stopped validator was recorded as `demotion_failed` and repeated every poll. Fixed (§5).
