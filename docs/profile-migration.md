# Profile Config Migration

This release makes `profiles:` the only supported way to declare HA groups.
Even a normal one-primary, one-backup deployment must declare one profile.

Old single-profile config is rejected at startup:

- `validator.identities.active`
- `validator.identities.active_pubkey`
- `failover.peers`
- `failover.priority`

Move the active identity, vote account, authorized voter, and peer map under
`profiles.<name>`. Keep the passive identity under `validator.identities`
because the daemon still controls one local validator process with one passive
identity.

## One Profile Migration

Before:

```yaml
validator:
  name: backup-validator
  rpc_url: http://127.0.0.1:8899
  identities:
    active: /keys/active-identity.json
    passive: /keys/passive-identity.json

failover:
  priority: 1
  peers:
    primary-validator:
      ip: 192.168.1.10
      priority: 0
    backup-validator:
      ip: 192.168.1.11
      priority: 1
```

After:

```yaml
validator:
  name: backup-validator
  rpc_url: http://127.0.0.1:8899
  identities:
    passive: /keys/passive-identity.json

profiles:
  main:
    priority: 0
    identities:
      active: /keys/active-identity.json
    vote_pubkey: Vote111111111111111111111111111111111111111
    authorized_voter: Auth111111111111111111111111111111111111111
    peers:
      primary-validator:
        ip: 192.168.1.10
        priority: 0
      backup-validator:
        ip: 192.168.1.11
        priority: 1

failover:
  active:
    command: /opt/solana-validator-ha/ha-set-role.sh
    args:
      - active
      - --active-identity-file
      - "{{ .ActiveIdentityKeypairFile }}"
      - --authorized-voter-pubkey
      - "{{ .AuthorizedVoterPubkey }}"
      - --passive-identity-file
      - "{{ .PassiveIdentityKeypairFile }}"
  passive:
    command: /opt/solana-validator-ha/ha-set-role.sh
    args:
      - passive
      - --passive-identity-file
      - "{{ .PassiveIdentityKeypairFile }}"
```

## Shared Backup Example

A shared backup opts into more than one profile by declaring each active
validator identity it is allowed to monitor and assume:

```yaml
validator:
  name: shared-backup-1
  rpc_url: http://127.0.0.1:8899
  identities:
    passive: /keys/shared-passive-identity.json

profiles:
  validator-a:
    priority: 0
    identities:
      active: /keys/validator-a-active.json
    vote_pubkey: ValidatorAVote111111111111111111111111111111
    authorized_voter: ValidatorAAuthorized11111111111111111111
    peers:
      validator-a-primary:
        ip: 192.168.1.10
        priority: 0
      shared-backup-1:
        ip: 192.168.1.50
        priority: 1

  validator-b:
    priority: 1
    identities:
      active: /keys/validator-b-active.json
    vote_pubkey: ValidatorBVote111111111111111111111111111111
    authorized_voter: ValidatorBAuthorized11111111111111111111
    peers:
      validator-b-primary:
        ip: 192.168.1.20
        priority: 0
      shared-backup-1:
        ip: 192.168.1.50
        priority: 1
```

`profiles.<name>.priority` decides which profile this daemon promotes if
multiple profiles need failover at the same time. Lower values win.

`profiles.<name>.peers.<peer>.priority` decides takeover order inside one
profile. Lower values win. Peer priorities are all-or-nothing per profile, so
if one peer declares a priority, every peer in that profile must declare one.

## Operational Notes

- All enabled profiles for one daemon must use the same local validator process
  and the same Solana cluster RPC.
- Each enabled profile must include this node in its `peers` map by
  `validator.name`, and the configured IP must match the node's discovered
  public IP.
- A daemon can actively serve only one profile at a time. If it is already
  active for one profile, failover for another profile is skipped as local busy.
- Every node that participates in a shared-backup deployment should know the
  other enabled active identities, so a peer serving another profile can be
  classified as busy instead of available.
- Automatic handback remains out of scope. A promoted backup stays active until
  HA demotes it after primary reassignment or an operator manually fails back.

## Role Script Change

Becoming active now requires both the selected profile identity and the selected
profile authorized voter. The example script accepts:

```bash
ha-set-role.sh active \
  --active-identity-file /keys/profile-active.json \
  --authorized-voter-pubkey Auth111111111111111111111111111111111111111 \
  --passive-identity-file /keys/passive.json
```

If you use a custom role script, update it so the active path sets or verifies
the authorized voter before reporting success. The HA daemon verifies local
identity after promotion; the script is responsible for making the validator
client's authorized-voter state match the selected profile.
