# Settlement lifecycle matrix

Measured by `scripts/localnet/settlement-lifecycle-matrix.sh`
(`make localnet-settlement-matrix`). Three Slots, three epochs, one chain.

Every other settlement run we have holds the validator set still. This one moves it
in both directions while settlements are outstanding, and runs long enough to cross
a deadline — the one boundary that changes *who* may finalize.

## The scenario

| epoch | what happens |
|---|---|
| 1 | Slot 1 from genesis; Slots 2 and 3 join within the first minute. Slot 2's operator is funded, Slot 3's deliberately is not. |
| 2 | Slot 2 pays its epoch-1 entitlement across every chunk it is allowed and finalizes early. Slot 3's operator, holding nothing, can do neither. Slot 3 is then inactivated mid-epoch, leaving an open settlement behind it. |
| 3 | Slot 3 is absent and earns nothing. The epoch-1 deadlines pass and an unrelated operator finalizes what is left. |

## What it establishes

**Allocation follows participation, and nothing is created or lost by churn.**
Each epoch's entitlements plus the change in carry equal the epoch emission exactly,
across a three-way split, a mid-epoch departure and an absence. Two Slots active for
a whole epoch earn *identically* — participation is credited by membership in the
active set, not by signatures, so there is nothing for network jitter to perturb.

**A settlement outlives its Slot.** Inactivating a Slot leaves its open settlement's
mode and the amount it owes untouched, and the settlement is still finalizable
afterwards — it pays out in full, to the payout address snapshotted when the epoch
closed. The debt is owed for an epoch that already closed; later lifecycle changes
cannot reach back into it.

**Settlement authority is per-Slot, and the deadline is not.** An operator is refused
when it tries to distribute another Slot's money, and permitted — the same
credential, the same chain — to push that same settlement to terminal once its
participant window has closed. Distributing is authority; finalizing after the
deadline is not.

**Both finalization arms are reached and recorded.** An early finalization by the
settlement's own signer records `AUTHORIZED_EARLY`; a post-deadline one by an
unrelated account records `PERMISSIONLESS_AFTER_DEADLINE`. The run asserts the arm,
not just the outcome — a finalization down the wrong branch looks identical from the
balances alone.

## The bootstrap question

**Does a Slot that joins after genesis need to be funded before it can be paid?**

No — but it needs to be funded before it can *pay anyone else*.

A Slot admitted after genesis has an address nobody has ever sent to, so it has no
account and cannot sign. Its entitlement is still real. But it cannot submit a chunk,
and it cannot finalize early either, because before the deadline only the settlement's
own signer may finalize and that also takes a signature. The money sits in escrow.

At the deadline the settlement opens to everyone. Any funded account can then finalize
it, and because no chunks were ever paid, the whole entitlement goes to the operator's
immutable payout snapshot as remainder. The operator now has an account and can sign.

So the chain funds a new operator out of its own earnings, with no help from anyone —
about two epochs late, and at the cost of that epoch's participants receiving nothing.
Pre-funding does not buy the money. It buys the ability to distribute in the first
epochs rather than forfeit them.

## Bounds exercised

Each negative case is built so the rule under test is the only thing wrong with the
transaction, and each is asserted against the message the chain returned rather than
against a bare non-zero code. A refusal that passes for the wrong reason reports a
rule as enforced that was never reached.

- payout lines out of **decoded-address-byte order** — refused (the bech32 text order
  is different, and sorting on it produces a chunk the chain rejects)
- a line one below the **minimum recipient payout** — refused
- the chunk after the last permitted one — refused by the **per-settlement cap**
- a chunk **at or past the deadline** — refused, leaving the settlement untouched
- a chunk or a second finalization **after finalization** — refused
- a refused chunk **consumes no index**: the next valid submission at that index is
  accepted

## Cost

Roughly fifteen minutes: three 360-block epochs at a 500ms commit, plus the 720-block
settlement window that has to elapse before the deadline arm exists. On-demand only,
like the other localnet runs — not a CI job.

## Related

- `scripts/localnet/join-and-settle.sh` — the same shape once, as a playbook
- [quorum-threshold-table.md](quorum-threshold-table.md) — what the validator set
  tolerates while all this is going on
