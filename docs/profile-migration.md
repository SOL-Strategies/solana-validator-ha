# HA profiles and shared backups

Existing single-group configurations continue to work. Opt into `profiles` when a node needs to back up multiple validator identities or when you want a named group with an explicit vote account.

## Migrate a node to named profiles

Keep the passive identity under `validator.identities`. Move the active identity into `profiles.<name>.identities`. Replace `failover.peers` and `failover.priority` with a peer map under the profile. Include the local node in that map using its `validator.name` and public IPv4 address. Use the same peer membership and peer priorities on all nodes in that group.

This example shows a shared backup. Keep the rest of your configuration, including the local and cluster RPC endpoints.

```yaml
validator:
  name: shared-backup
  identities:
    passive: /keys/shared-backup-passive.json

profiles:
  validator-a:
    priority: 0
    identities:
      active: /keys/validator-a-active.json
    vote_pubkey: Vote111111111111111111111111111111111111111
    authorized_voter: SysvarRent111111111111111111111111111111111
    peers:
      primary-a:
        ip: 192.0.2.10
        priority: 0
      shared-backup:
        ip: 192.0.2.50
        priority: 1
  validator-b:
    priority: 1
    identities:
      active: /keys/validator-b-active.json
    vote_pubkey: Stake11111111111111111111111111111111111111
    authorized_voter: SysvarRecentB1ockHashes11111111111111111111
    peers:
      primary-b:
        ip: 192.0.2.20
        priority: 0
      shared-backup:
        ip: 192.0.2.50
        priority: 1

failover:
  active:
    command: /opt/solana-ha/activate-profile
    args:
      - "{{ .ProfileName }}"
      - "{{ .ActiveIdentityKeypairFile }}"
      - "{{ .VoteAccountPubkey }}"
      - "{{ .AuthorizedVoterPubkey }}"
  passive:
    command: /opt/solana-ha/demote-profile
    args:
      - "{{ .ProfileName }}"
      - "{{ .PassiveIdentityKeypairFile }}"
```

Replace the example paths, IPs, and public keys with your deployment values. Use `active_pubkey` in place of `active` when your role command resolves the active keypair itself. Set `enabled: false` on an entry to disable it.

Lower profile priorities win when this daemon can take over more than one group at once. The separate peer priorities determine takeover order within a group. Omit every peer priority in a group to use the IP-based order. A node needs one local validator process, one passive identity, and one cluster for all its enabled profiles.

On a dedicated primary, configure the group it serves and list other active identities that its shared backups may hold. For example, primary A can add this top-level mapping with validator B's real active public key.

```yaml
known_active_identities:
  validator-b: SysvarC1ock11111111111111111111111111111111
```

This mapping marks a peer as busy when its other active identity appears in gossip. The dedicated primary can use it with either config layout. A busy peer is unavailable as a backup, including while its previous passive identity still appears in gossip.

## Role commands on Agave, Firedancer, and FireBAM

The named profile's commands and pre/post hooks can use `.ProfileName`, `.ProfilePriority`, `.VoteAccountPubkey`, and `.AuthorizedVoterPubkey` alongside the existing active and passive identity fields. Supply executable commands for your deployment. The paths in the example refer to operator-provided commands.

Before returning success from activation, configure and verify the selected vote account, authorized voter, and any consensus signing material required by the client. Preserve the selected validator's vote history and use the deployment's fencing or handoff procedure. Return an error if any part fails. The daemon checks the resulting identity through local RPC. Its promotion check also requires a matching local genesis on Alpenglow.

The [bundled identity-switch example](../example-scripts/ha-set-role.sh) is for a vote account and signing setup already in place. The full Firedancer and FireBAM activation path accepts a signed vote-history file for a manual identity switch. Use the client's coordinated handoff when moving between failover-enabled Firedancer instances. The Agave/Jito-Solana example handles a legacy TowerBFT tower file. Use your deployment's vote-history handling for Alpenglow. [Firedancer failover guide](https://docs.firedancer.io/guide/failover.html) · [FireBAM setup guide](https://jito-foundation.gitbook.io/mev/jito-solana/firebam-setup-guide)

A shared backup that promotes stays with that profile until it is demoted. Handback is an operator action.
