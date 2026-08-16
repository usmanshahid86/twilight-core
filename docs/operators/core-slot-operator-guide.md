# Core-Slot Operator Guide

CoreSlot is a greenfield Cosmos SDK / CometBFT **Proof-of-Authority** chain.
Validator admission and validator-set updates are owned exclusively by
`x/coreslot`. **Staking, distribution, slashing, mint, and governance are
omitted.** There is **no automatic slashing or jailing** — see
[Offline validator behavior](#offline-validator-behavior) and
`docs/security/coreslot-evidence-policy.md`.

## Key facts before you start

- **A bare `twilightd init` genesis is INVALID until active core slots are added.**
  The default genesis has `MinActiveSlots = 1` and zero slots, so
  `ValidateGenesis` fails until you add slots with `coreslot-genesis add`.
- **Authority actions are on-chain transactions.** Registration / activation /
  inactivation / suspension / removal / key rotation / params are signed
  messages, gated by the on-chain normal authority (or, for suspend only, the
  emergency authority).
- **`activation_delay_blocks` and `removal_delay_blocks` params are reserved and
  UNUSED in v1.** Only `key_rotation_delay_blocks` is honored; activation,
  inactivation, suspension, and removal take effect in the including block's
  EndBlock. Do not rely on activation/removal delays in v1.
- **Reward weight is separate from consensus power** and has no payout effect in v1.
  Rewards are allocated by active-block participation in `x/rewards`; configured reward
  weight is snapshotted but not used for allocation.

## 1. Localnet / devnet startup

Build and initialize a four-validator network:

```bash
make build
make localnet-init               # builds, inits 4 nodes, derives the 4 active slots
./scripts/localnet/start.sh      # starts all four nodes (plain logs, no ANSI color)
```

Stop it with `./scripts/localnet/stop.sh`. Node homes are at
`/tmp/twilight-localnet/node0` … `node3`; logs at `/tmp/twilight-localnet/logs/`.

`make localnet-init` derives each slot's consensus key from that node's generated
`priv_validator_key.json`, sets `operator0` as the normal authority and
`operator1` as the emergency authority, and funds both authority accounts so
their transactions are signable (fees are zero — min-gas `0utwlt`).

Full smoke (init + start + cross-node hash agreement):

```bash
make localnet-smoke
```

The manual genesis workflow (for a fresh chain) is:

```bash
twilightd init node0 --chain-id twilight-1
twilightd coreslot-genesis set-authorities <authority> <emergency-authority>
twilightd add-genesis-account <authority> 1000000000000utwlt
twilightd coreslot-genesis add <operator> <payout> <settlement> <ed25519-pubkey-base64> <moniker>
twilightd coreslot-genesis validate
twilightd start
```

`<settlement>` is the slot's settlement address — the operational credential the
operator signs settlement-side messages with. It is mandatory and has no default;
it may be the same account as the operator or payout address, which is what the
localnet scripts do.

`add` also writes the slot's initial Selection policy. Both values are operator
configuration with no protocol significance, and the command supplies defaults if
you omit the flags:

```bash
--selection-rate-bps 2500              # initial selection rate, basis points
--max-selected-participants 10         # initial per-slot cap on selected participants
```

## 2. Common signing flags

Authority-gated messages are signed by the **normal authority** key
(`operator0`); `suspend` is signed by the **emergency authority** key
(`operator1`). On the localnet:

```bash
# normal authority (operator0 lives in node0's keyring)
AUTH="--from operator0 --keyring-backend test --home /tmp/twilight-localnet/node0 \
      --chain-id twilight-localnet-1 --node tcp://127.0.0.1:26657 --fees 0utwlt --gas 600000 -y"
# emergency authority (operator1 lives in node1's keyring)
EMER="--from operator1 --keyring-backend test --home /tmp/twilight-localnet/node1 \
      --chain-id twilight-localnet-1 --node tcp://127.0.0.1:26657 --fees 0utwlt --gas 600000 -y"
```

## 3. Adding a new core-slot operator

A new slot needs a unique operator address, a settlement address and a fresh
consensus key:

```bash
twilightd keys add newop --keyring-backend test --home /tmp/twilight-localnet/node0
NEWOP=$(twilightd keys show newop -a --keyring-backend test --home /tmp/twilight-localnet/node0)
PUB=$(./scripts/localnet/gen-consensus-key.sh newslot | cut -f1)   # fresh ed25519 pubkey (base64)
# operator, payout and settlement may be the same account; settlement is mandatory.
twilightd coreslot register "$NEWOP" "$NEWOP" "$NEWOP" "$PUB" "moniker" $AUTH
```

Registration also records the slot's initial Selection policy. As with genesis
authoring these are operator configuration, not protocol constants, and the
command supplies defaults when the flags are omitted:

```bash
--selection-rate-bps 2500              # initial selection rate, basis points
--max-selected-participants 10         # initial per-slot cap on selected participants
```

The slot is created in `SLOT_STATUS_PENDING` (power 0, not yet in the validator
set). `gen-consensus-key.sh` writes a throwaway node home and prints the new
pubkey plus the path to its `priv_validator_key.json` (needed when that key will
actually sign — see [key rotation](#7-key-rotation)).

## 4. Activation

```bash
twilightd coreslot activate <slot-id> $AUTH
```

The slot becomes `ACTIVE` at `SlotVotingPower`; one validator-update is emitted
in that block's EndBlock (`num_val_updates=1`). Bounded by `MaxActiveSlots`.

## 5. Inactivation

```bash
twilightd coreslot inactivate <slot-id> "<reason>" $AUTH
```

Sets the slot `INACTIVE`, power 0, removed from the validator set
(`num_val_updates=1`). Rejected if it would cross `MinActiveSlots`
(`ErrMinActiveSlots`) — the active set can never be drained to empty. An operator
may self-inactivate its own slot subject to the same floor.

## 6. Emergency suspension

```bash
twilightd coreslot suspend <slot-id> "<reason>" "<evidence-reference>" $EMER
```

Signed by the emergency authority. Sets the slot `SUSPENDED`, power 0. A hard
floor of one active validator always applies (`ErrCannotRemoveLastValidator`);
crossing `MinActiveSlots` requires `allow_emergency_below_min_active = true` in
params. Always include an `evidence-reference` (see the evidence policy).

## 7. Removal

```bash
# the slot must be NON-active first (inactivate or suspend it)
twilightd coreslot inactivate <slot-id> "decommission" $AUTH
twilightd coreslot remove <slot-id> "decommission" $AUTH
```

Removing an already-non-active slot has **no validator-set effect**
(`num_val_updates=0`) — it was not in the set. The slot becomes `REMOVED`
(terminal) and its consensus key is reserved for `consensus_key_reuse_lockout`
blocks. Removing an `ACTIVE` slot is rejected.

## 7. Key rotation

```bash
KEYOUT=$(./scripts/localnet/gen-consensus-key.sh rotated-slot)   # "<pubkey>\t<priv_validator_key.json path>"
NEWPUB=$(cut -f1 <<<"$KEYOUT"); NEWKEYFILE=$(cut -f2 <<<"$KEYOUT")
twilightd coreslot rotate-key <slot-id> "$NEWPUB" $AUTH
```

For an **active** slot the rotation is delayed by `key_rotation_delay_blocks`
(default 1) and applied in EndBlock: the old key leaves at power 0 and the new
key enters at `SlotVotingPower` in one atomic step (`num_val_updates=2`). The old
key is reserved for the lockout. A second rotation while one is pending is
rejected (`ErrPendingRotationExists`).

> **Important:** rotating an *online* validator's key makes the node that still
> holds the OLD key stop being a signer. To keep that node validating, follow the
> restart-after-rotation procedure below.

## 8. Restart after key rotation

When you rotate the key of a slot whose node is online, swap that node's key
material and restart it so it signs with the new key:

```bash
twilightd coreslot rotate-key <slot-id> "$NEWPUB" $AUTH   # wait until applied (pending-rotations empty)
./scripts/localnet/stop.sh                            # or stop just that node's process
cp "$NEWKEYFILE" /tmp/twilight-localnet/node<N>/config/priv_validator_key.json
# restart that node (start.sh restarts all; or start the single node process)
```

Leave `priv_validator_state.json` in place — the current height is already past
the last-signed height, so there is no height regression. The node rejoins and
signs for the slot with the new key. This is automated and proven by
`make drill-restart-rotation`.

## 9. Quorum / halt recovery

Equal-power four-node set: any **3 of 4** online (> 2/3 power) keeps producing
blocks; **2 of 4** (≤ 2/3) halts **safely** (no fork, no app-hash divergence).
This is correct liveness behavior, not a failure.

Recovery from a halt is **not** an on-chain transaction (the chain cannot commit
while halted): bring the **same** validators back online with the **same** keys
and state, and the chain resumes from the last finalized height. Proven by
`make drill-quorum`.

## 10. Offline validator behavior

An offline validator's slot stays `ACTIVE` at `SlotVotingPower`. **There is no
automatic jailing, slashing, or power reduction** — `x/slashing` is omitted, and
no CoreSlot path mutates power automatically. Power changes only via an explicit
lifecycle transaction. If an operator is durably offline, an authority may
`inactivate` or (emergency) `suspend` the slot deliberately.

## 11. Evidence response

For double-sign / equivocation, follow `docs/security/coreslot-evidence-policy.md`:
detect → preserve evidence → emergency-suspend if risk is immediate → notify →
investigate → reactivate or remove through the authority path → record the
incident. CometBFT evidence never auto-mutates CoreSlot power in v1.

## 12. Expected localnet drill commands

```bash
make localnet-smoke           # init + start + four-node hash agreement
make drill-lifecycle          # CLI-driven live lifecycle e2e + provenance guard
make drill-restart-rotation   # rotate key, swap node key, restart, rejoin
make drill-quorum             # 3/4 continue, 2/4 halt, resume
make drills                   # all three drills in sequence
make localnet-agree           # ad-hoc hash agreement on a running localnet
```

Each drill spins up its own four-node localnet and tears it down on exit. By
default it creates a UTC timestamp + PID run ID and writes evidence under
`build/localnet/evidence/<run-id>/<drill>/`. Set `RUN_ID=<id>` to choose a run
ID explicitly. `build/localnet/evidence/latest-run.txt` records the most recent
run ID. `make drills` shares one run ID across all three drill directories.

## 13. How to read evidence output

Every drill directory contains:

- `summary.csv` — one compact row per action/checkpoint;
- `hashes.jsonl` — one machine-readable record per checked node and action,
  including the checked subset, common comparison height, node latest height,
  app hash, validators hash, next-validators hash, agreement result, and
  timestamp;
- `provenance.jsonl` — one record per relevant node and effective height,
  including actual/expected `num_val_updates` and PASS/FAIL.

The restart-rotation drill also writes `rotation.jsonl` and
`rotation-addresses.txt` with slot/operator/transaction/effective-height data
and exact old/new post-rotation powers.

`summary.csv` columns:

| column | meaning |
|---|---|
| `action` | drill step label |
| `height_before` / `height_after` | node0 height bracketing the action |
| `tx_hash` / `tx_code` | the lifecycle transaction and its DeliverTx code (`0` = success) |
| `active_count` | number of `ACTIVE` slots after the action |
| `num_val_updates` | CometBFT validator updates emitted at the effective block |
| `agree` | four-node app/validators/next-validators hash agreement (PASS/FAIL) |
| `result` | overall pass/fail for the action |
| `checked_slot_id` / `checked_slot_status` / `checked_slot_power` | focused slot-state assertion, used by the offline-validator checkpoint |

**Expected `num_val_updates`:** ordinary/quiet blocks `0`; register `0` (slot is
PENDING); a single membership change (activate / inactivate / suspend / reactivate)
`1`; removal of a non-active slot `0`; **active key rotation `2`** (old@0 +
new@power). Every relevant node must contain a valid count equal to the expected
value; missing, malformed, or inconsistent node logs fail the drill. Any other
emitter is impossible — `x/coreslot` is the only EndBlock validator-update
source.

## 14. Interpreting hash agreement

`scripts/localnet/agree.sh` compares, across nodes at a common height:

- **`app_hash`** — identical application state. Divergence here is the
  catastrophic class (a silent state fork).
- **`validators_hash`** — the active validator set in effect at height H.
- **`next_validators_hash`** — the set that takes effect at H+1.

A CoreSlot set change applied in EndBlock at H shows up in `next_validators_hash`
at H and in `validators_hash` at H+1 (a one-block lag). Comparing the **same**
height across nodes is lag-safe — every correctly configured node computes the
same value for that H, so a mismatch is a real divergence. `agree.sh` exits
nonzero on any divergence, missing node, or non-progressing node. Set
`AGREE_NODES="0 2"` to check agreement among a live subset (used by the quorum
drill during a halt).
