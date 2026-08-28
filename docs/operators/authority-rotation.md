# Rotating an authority

Audience: whoever holds the authority key, or is about to.

Twilight Core has two operational authority roles, both ordinary signable accounts recorded in
`x/coreslot` parameters:

| role | CLI name | gates |
|---|---|---|
| primary | `primary` | validator admission, parameter updates, upgrade scheduling |
| emergency | `emergency` | pausing and resuming rewards |

Handing either to another key is a **two-step** operation. The incumbent nominates; the
successor accepts by signing with the key it must actually hold. Nothing changes until it does.

Reproduce the whole flow with:

```bash
make localnet-authority-rotation-drill
```

---

## Why two steps

A single-step rotation is unrecoverable when it goes wrong, and it goes wrong in ordinary ways.

The primary authority gates validator admission, parameter updates, **and upgrade scheduling** —
`ScheduleUpgrade` reads the same parameter from chain state. So a chain that loses its authority
cannot upgrade its way out of having lost it. There is no governance module to appeal to.

Requiring the destination to sign means a wrong-but-valid address can never take the role. A typo
is inert and correctable rather than terminal.

**What this does not do:** it does not protect you against someone who already holds your
authority key. There is no timelock, so an attacker nominates an address they control and accepts
in the next block. You are guaranteed no reaction window. Protect the key itself — a k-of-n
multisig account works here with no chain change.

---

## Rotating

### 1. Nominate

Signed by the **current holder** of that role:

```bash
twilightd tx coreslot nominate-authority primary twilight1<successor> \
  --from <current-authority-key> --chain-id <chain-id>
```

Nothing has changed yet. The incumbent still holds every capability, and the nominee holds none.
Verify that before continuing — an incumbent that has already lost the role is a different and
much worse situation than a pending nomination.

The nominee is checked at this point. A module account, a bank-blocked address or the all-zero
address is refused outright: nobody can sign for those, so installing one would end the role
permanently.

### 2. Accept

Signed by the **nominee**, not the incumbent:

```bash
twilightd tx coreslot accept-authority primary \
  --from <successor-key> --chain-id <chain-id>
```

On success the role transfers, the pending nomination is cleared, and the former holder loses the
capability immediately.

The successor account must exist on chain before it can sign. Fund it first, even with a trivial
amount — an unfunded key cannot submit anything, and the failure looks like an authorization
error rather than a missing account.

### Changing your mind

Either withdraw the nomination:

```bash
twilightd tx coreslot cancel-authority-nomination primary --from <current-authority-key> ...
```

or simply nominate someone else — a new nomination replaces the pending one for that role, and
the displaced nominee can no longer accept.

Both are signed by the current holder, which is what makes a mistaken nomination survivable.

---

## What cannot happen

- **A nomination cannot move the role.** Only acceptance does.
- **Only the exact nominee can accept.** Not the incumbent, not a previous nominee.
- **`update-params` cannot rotate anything.** A parameter document whose `authority` or
  `emergency_authority` differs from current state is **rejected**, not silently corrected. This
  is the case the design exists for: before it, editing `max_active_slots` meant re-supplying both
  authority addresses correctly, and getting one wrong was unrecoverable.
- **The two roles are independent.** Rotating one leaves the other untouched, and the holder of
  one cannot nominate for the other.

---

## Verifying

The rotation is visible in parameters:

```bash
twilightd coreslot-query params --output json | jq '.params | {authority, emergency_authority}'
```

There is currently **no query for a pending nomination**. Until one exists, a nomination is
visible in the transaction's events (`coreslot_authority_nominated`, carrying the role, the
nominating authority and the nominee), and in an exported genesis document under
`app_state.coreslot.pending_authority_transfers`.

> **Note on `update-params`:** the output of `coreslot-query params` cannot currently be fed
> straight back into `coreslot update-params` — the query renders numbers as JSON strings and the
> command expects unquoted numbers. Convert them before submitting. Tracked separately.

---

## Genesis

A fresh genesis carries no pending nominations, and `coreslot-genesis set-authorities` sets both
roles directly. Note that a genesis produced by plain `twilightd init` seeds both fields with
**module addresses**, which nobody can sign for — a chain launched without running
`set-authorities` is ungovernable from block one.

Pending nominations survive export and import, so a captured state does not strand a rotation
that was in flight.
