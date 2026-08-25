# ADR-0003: Upgrade path — authority-gated `x/upgrade`, without governance

- **Status:** Accepted — decision recorded. The wiring, genesis change and upgrade drill are
  tracked in [#131](https://github.com/usmanshahid86/twilight-core/issues/131).
- **Date:** 2026-08-21
- **Relates to:** [architecture overview](../overview.md),
  [ADR-0001](0001-coreslot-poa.md) (the authority model this reuses)

## Summary

The chain wires the standard `x/upgrade` module with the **CoreSlot authority** as its
signer. No governance module is introduced, and none is required: `x/upgrade`'s authority is
configurable, and this application already configures `auth`, `bank` and `consensus` the same
way.

Two classes of change are recognised, with two different procedures. Node-local changes roll
one validator at a time with no downtime. State-machine changes halt every node at an agreed
height. Conflating the two is how a chain forks, so the distinction is written down rather
than left to judgement.

## Context

There is no `x/upgrade` module today. The wired set is `auth`, `bank`, `consensus`,
`coreslot`, `rewards`, `mining`, `tx` and `runtime`. There is no upgrade store, no
`SetUpgradeHandler`, and no on-chain upgrade height.

The consequences are concrete:

- **No agreed halt.** Nothing stops the network at a known block, so a binary swap is an
  out-of-band arrangement. Operators who act early or late run different state-machine logic
  at the same height, which is a fork, not a delay.
- **No migration hook.** A change to any stored layout has no supported path from old state to
  new.
- **The only recovery tool is export**, which is itself unproven for a mid-life chain.

Three things make the decision urgent rather than merely open.

**Timing.** Adding `x/upgrade` after genesis costs a relaunch. Adding a module adds a store,
and migrating stores is precisely what `x/upgrade` exists to perform — so the module cannot
bootstrap itself. It is cheap before genesis and expensive at every moment after.

**The chain is going public.** Every future coordinated event gets more expensive once there
are users who did not agree to be part of an experiment.

**The chain is Proof-of-Authority by design.** `staking`, `distribution`, `gov`, `mint` and
`slashing` are intentionally omitted, and that omission is a stated hard invariant rather than
an oversight. Any upgrade mechanism has to fit that model instead of quietly replacing it.

## Decision

### 1. Wire `x/upgrade`, reached only through the CoreSlot authority

`x/upgrade` defaults its authority to the governance module account, which is why the module
is often assumed to depend on governance. It does not — the authority is configurable. But
configuring it naively does not work here, and the reason is worth recording because it is
invisible until an upgrade is attempted.

The authority this application passes to `auth`, `bank` and `consensus` is
`AuthorityModuleName`, which is a **module account**. Module addresses are derived from a
name, so **no private key exists for them**. For those three modules that is harmless:
their `MsgUpdateParams` is simply unreachable, and their parameters are static. Wiring
`x/upgrade` the same way would produce an upgrade module to which **no account could ever
submit a plan** — the keeper fixes its authority at construction, with no setter, and its
message server compares the signer against that value directly.

The operational authority is not that module account. It is `Params.authority` in
`x/coreslot` — a genesis-set account address, held by a real key, and rotatable. The
constraint is that the two cannot see each other: the application configuration is a
compile-time value, while `Params.authority` is chain state read at run time.

So the upgrade module keeps the keyless module address as its authority, **deliberately**, and
the only path to it is a proxy:

```
MsgScheduleUpgrade / MsgCancelUpgrade   (x/coreslot)
  signer checked against Params.authority
        |
        v
  upgradeKeeper.ScheduleUpgrade(plan) / ClearUpgradePlan(ctx)
```

This is the same shape governance uses to execute a proposal, and it keeps **one** authority
concept rather than two. Rotating `Params.authority` rotates who may upgrade, with nothing to
keep in sync, and the two-step rotation hardening covers the upgrade path for free.

The alternative — compiling a real account address into the binary as the upgrade authority —
was rejected. It creates a second authority that can drift from the first, sits outside the
rotation hardening, requires a different binary per network or the same key across networks,
and can itself only be changed by performing an upgrade.

### 1a. `PreBlockers` must list `upgrade`

`x/upgrade` executes a scheduled plan in **PreBlock**, before any `BeginBlocker`. The runtime
configuration in this application did not previously set `PreBlockers` at all. Omitting the
entry is silent and total: plans are still accepted, still stored and still queryable, but
nothing ever applies them, so the chain runs straight past its own halt height. The upgrade
drill asserts the halt rather than trusting review to catch this.

### 2. Do not introduce a governance module

Recorded as a decision, not an omission. See *Alternatives considered*.

### 3. Two classes of change, two procedures

| | node-local | state-machine |
|---|---|---|
| examples | pruning, RPC and API config, indexer, log level, p2p tuning, metrics, hardware | `x/coreslot`, `x/rewards`, `x/mining`, `app/` wiring, parameter structure, proto, the module-account set |
| procedure | roll one validator at a time | halt every node at an agreed height |
| downtime | none | seconds |
| mechanism | none needed | `x/upgrade` |

The left column genuinely delivers zero-downtime upgrades and covers most routine operations.
The right column cannot: if two nodes run different state-machine logic at the same height
they compute different application hashes. That is a property of replicated state machines,
not a missing feature.

### 4. Cosmovisor on every validator, with automatic download disabled

Cosmovisor turns "coordinate a restart across every operator" into "every node comes back on
its own", reducing a coordinated halt to seconds.

`DAEMON_ALLOW_DOWNLOAD_BINARIES` stays **false**. A node holding real balances must never
fetch and execute a binary named by an on-chain message. Binaries are pre-staged and their
hashes verified out of band.

### 5. Scheduling constraints

- **Never schedule an upgrade at an epoch boundary.** Epochs are 360 blocks. The upgrade
  handler runs at the beginning of the upgrade height and rewards finalization runs at its
  end, so a boundary height puts a migration and an epoch close in the same block, on the
  money-critical path. Schedule comfortably mid-epoch.
- Pre-stage and hash-verify binaries before the plan is submitted, never after.
- For a migration touching rewards or mining state, consider pausing rewards first. The
  emergency authority already provides that primitive: it stops accrual and release together
  and freezes the settlement clock, giving a clean quiescent state to migrate from.

### 6. Authority accounts may be multisig

`Params.authority` and `Params.emergency_authority` are ordinary account addresses set at
genesis. Either may be a k-of-n multisig account with **no chain change at all**. Running the
normal authority at a higher threshold than the emergency authority — deliberate versus fast —
is an operational choice this decision endorses but does not encode.

## Consequences

**A consensus-affecting upgrade always halts the chain.** This decision does not remove that;
it makes the halt deterministic, brief and automatic instead of manual and error-prone. Nodes
that did not upgrade halt rather than fork, which is the correct fail-closed outcome.

**Upgrade risk is coupled to validator-set size.** The chain resumes only once quorum returns,
and quorum needs more than two-thirds of voting power. At one, two or three validators that
means *every* validator must complete the swap — one operator fumbling keeps the network down.
At four, three suffice and the fourth can catch up afterwards. This is an independent argument
for launching public with at least four validators, alongside the fault-tolerance one.

**Settlement deadlines are safe across a halt.** The settlement clock advances only on blocks
that were produced, so a halt freezes every open participant window rather than consuming it.
Nothing expires while the network is down, however long that is. This property should be
asserted by the upgrade drill rather than assumed.

**Migrations inherit the fail-closed rule.** An upgrade handler runs on the same terms as
`BeginBlock` and `EndBlock`: in a cache context, committing only on complete success. A
half-applied migration on a chain carrying outstanding entitlement liability is not
recoverable by retry.

**The upgrade path inherits the authority's weaknesses.** Whatever can compromise or misdirect
the authority now also reaches the upgrade mechanism. That raises the value of hardening
authority rotation, tracked in
[#130](https://github.com/usmanshahid86/twilight-core/issues/130).

**`--unsafe-skip-upgrades` is a deliberate exception to the determinism invariant.**
`AGENTS.md` invariant 4 says no state transition may read node-local config. x/upgrade's
PreBlocker consults `IsSkipHeight`, populated from that node-local flag, and when it matches it
performs a store delete (`ClearUpgradePlan`). Two nodes started with different values therefore
compute different state at the same height.

This is the SDK's standard emergency escape hatch and it is accepted here rather than
engineered away — but it is an exception, not an oversight, and it is recorded as one. The flag
is only safe when the whole network has agreed to skip the same height; used unilaterally it is
a fork. Operator documentation must say so.

**Adding the upgrade store is itself unrecoverable for an existing chain.** Wiring the module
mounts a new IAVL store, so a chain with committed state started before this change cannot boot
on a binary that has it — the root store reports a version mismatch for the new key and refuses
to load. There is no migration path, because the mechanism that would schedule the store
addition is the very module being added. Any chain predating this wiring, twilight-devnet-2
included, must be replaced rather than upgraded. That is the same one-way door this record is
about, seen from the other side.

**Export and restore become the disaster path.** Wiring `x/upgrade` does not remove the need
for a proven export; it makes it the fallback for when resuming is not an option. It must be
exercised on a mid-life chain, not only at genesis.

## Alternatives considered

**A — Accept manual coordinated restarts.** Rejected. Without an agreed height there is no
moment at which the change takes effect for everyone, so operators acting at different times
fork or halt at different points. It also leaves no migration hook, which makes any
stored-layout change a relaunch rather than an upgrade. Defensible for a private proof of
concept among cooperating operators; not defensible for a public network.

**B — Wire `x/gov` and move authority privileges to it.** Rejected, and not merely on
preference: it does not drop in.

`x/gov` derives voting power from staking. It requires a staking keeper for total bonded
tokens, bonded-validator iteration, delegations and the bond denomination. This chain has no
staking module, deliberately. That leaves two sub-options, both bad:

- *Wire staking too.* The chain stops being Proof-of-Authority, violates two hard invariants,
  and inherits delegation, unbonding and slashing semantics that were excluded on purpose.
- *Write a voting-power adapter over CoreSlot.* This is building a bespoke governance module,
  not adopting a standard one — new consensus-critical surface immediately before a public
  launch.

Two further objections apply even if it did drop in cleanly. Governance is **slow by
construction**, with voting periods measured in days, while the emergency pause must stay
immediate; the emergency authority would therefore survive anyway, leaving governance's
complexity *and* a privileged key. And **stake-weighted voting on a chain where no stake is at
risk confers a legitimacy it has not earned** — it resembles distributed governance while
resolving to whoever holds the most tokens. A named, accountable authority is the more honest
description of who decides, and a multisig addresses the concentration concern directly.

**C — A minimal CoreSlot-weighted governance module.** *Deferred, not rejected.* One active
slot, one vote, with no token weighting and no deposits. This is the right shape if and when
independent operators need a formal say in decisions that affect them. It is a distinct
decision with its own design and review, and it is not a prerequisite for launch.

## Enforcement

- The hard-invariant list is unchanged: `staking`, `distribution`, `gov`, `mint` and
  `slashing` stay omitted. `x/upgrade` is added to the wired set; nothing is removed from the
  omitted one.
- An upgrade is exercised end to end on a localnet before it is exercised in production:
  schedule at a height, all nodes halt, binaries swap, the chain resumes, and every node
  agrees on the application hash afterwards. A settlement is left open across the boundary to
  demonstrate the clock froze and its deadline did not move.
- The two-class rule is published in the operator documentation, so classification is a
  written rule rather than a per-change judgement.
