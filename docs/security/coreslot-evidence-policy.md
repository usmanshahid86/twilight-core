# CoreSlot v1 Evidence / Equivocation Policy

_Status: v1 operational policy. Applies to the greenfield CoreSlot PoA chain
(staking, distribution, and slashing omitted)._

## 1. Principle

CoreSlot v1 has **no automatic, consensus-level punishment**. Validator power is
changed **only** by explicit, authority-signed CoreSlot lifecycle transactions.
Misbehavior response is an **operational/authority decision**, not an on-chain
automatic reaction.

## 2. What v1 does NOT do (by design)

- **`x/slashing` is omitted.** There is no jailing, no slash, no automatic
  downtime penalty, no stake burning (there is no bonded stake at all).
- **CometBFT evidence does not mutate CoreSlot power.** Duplicate-vote /
  light-client evidence reported by CometBFT is recorded by the consensus engine
  but is **not** wired to any module path that changes a slot's status or
  `ConsensusPower`. No CoreSlot EndBlock logic reads evidence.
- **Downtime does not jail or reduce power.** An offline validator keeps its slot
  `ACTIVE` at `SlotVotingPower` until an authority explicitly changes it. (Proven
  by the offline-validator drill: `scripts/localnet/quorum-drill.sh`,
  action `02-offline-no-auto-change`.)
- **No operator reward forfeiture exists in CoreSlot.** Consensus power and reward
  configuration are independent: the operator reward weight (`OperatorRewardWeight`) is
  separate from consensus power and is not consumed by CoreSlot. CoreSlot reward metadata
  must not affect validator-set derivation or emitted `ValidatorUpdate`s. In v1, rewards are
  allocated by active-block participation in `x/rewards`; configured reward weight is
  snapshotted but unused. Any future reward forfeiture would be an `x/rewards` policy, not
  CoreSlot validator-set logic.

## 3. How v1 responds to equivocation / double-sign

Equivocation response is **authority / emergency-authority driven, out of band**:

- The **emergency authority** may immediately remove a misbehaving validator from
  the active set with `MsgSuspendCoreSlot` (`twilightd coreslot suspend <slot-id>
  <reason> <evidence-reference>`). Suspension sets power to 0 in the next EndBlock.
- The suspend message **should** carry an `evidence_reference` (e.g. a block
  height + the CometBFT evidence hash, or an incident ticket id) so the on-chain
  record points at the off-chain evidence.
- The hard floor still applies: suspension cannot drain the active set to zero
  (`ErrCannotRemoveLastValidator`), and crossing `MinActiveSlots` requires
  `AllowEmergencyBelowMinActive`.
- **Final disposition** — permanent removal (`MsgRemoveCoreSlot`, after the slot
  is non-active) or reinstatement (`MsgActivateCoreSlot`) — is a deliberate
  **normal-authority** operational decision made after investigation.

## 4. Where automated evidence handling lives (future, not v1)

An automated evidence/downtime **observer** (a module or off-chain bot that
watches CometBFT evidence / missed-signature data and proposes or triggers a
suspend) is explicitly a **future component**. It is **not** part of v1 consensus
logic, and it must never bypass the authority model: it can at most *recommend*
or *submit* an authority/emergency suspend transaction through the normal path.

## 5. Recommended operational procedure

When equivocation or serious misbehavior is suspected:

1. **Detect** — observe the CometBFT evidence (double-sign / light-client) or the
   missed-block / divergence signal. The localnet hash-agreement tooling
   (`scripts/localnet/agree.sh`) detects app-hash / validator-hash divergence.
2. **Preserve** — capture the offending node's logs, the evidence record (height,
   evidence hash), and the divergence artifacts. Do not discard state.
3. **Emergency suspend if risk is immediate** — emergency authority submits
   `twilightd coreslot suspend <slot-id> "<reason>" "<evidence-reference>"`. Confirm
   the slot leaves the active set in the next block (`coreslot-query slot
   <slot-id>` shows `SLOT_STATUS_SUSPENDED`, power 0; one validator-update emitted).
4. **Notify operators** — inform the other CoreSlot operators and stakeholders of
   the suspension and the reason.
5. **Investigate** — determine root cause: malicious double-sign, key compromise,
   misconfiguration, or false positive.
6. **Resolve through the authority path** —
   - *Reinstate*: if benign, the normal authority reactivates with
     `twilightd coreslot activate <slot-id>` (optionally after a key rotation).
   - *Remove*: if malicious/compromised, the normal authority permanently removes
     with `twilightd coreslot remove <slot-id> "<reason>"` (the slot must already be
     non-active; its consensus key is then reserved for the lockout window).
   - *Rotate*: on suspected key compromise, rotate the consensus key
     (`twilightd coreslot rotate-key`) and follow the restart-after-rotation runbook.
7. **Record the incident** — keep the `evidence_reference`, the transactions, and
   a written incident summary for audit.

## 6. Cross-references

- Architecture: `docs/architecture/coreslot-poa.md` (validator ownership,
  authority model, reward/power separation).
- Operator runbook: `docs/operators/core-slot-operator-guide.md` (evidence
  response, emergency suspension, removal, key rotation, restart).
- Drills proving the no-auto-punishment behavior:
  `scripts/localnet/quorum-drill.sh` (offline-no-auto-change),
  `scripts/localnet/lifecycle-e2e.sh` (suspend via emergency authority).
