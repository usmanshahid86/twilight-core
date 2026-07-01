---
title: Module Accounts
---

# Module Accounts

Declared in `app/config.go` under the auth module's `ModuleAccountPermissions`,
created at genesis. See also [Accounts & Denoms](../chain/accounts-and-denoms.mdx).

| Account | Permission | Holds | Used by |
|---|---|---|---|
| `rewards` | `Minter` | minted rewards before allocation; unclaimed rewards and carry-forward after finalization | mint at finalization; send on claim/treasury |
| `rewards_fee_pool` | _(none)_ | nothing (dormant) | reserved for future fee plumbing |

## How each account is used

- **`rewards`** is the only `Minter` on the chain. At finalization the bank
  keeper mints the epoch emission into it; claims send from it to payout
  addresses; the optional treasury transfer sends from it. The
  module-balance-coverage invariant requires its balance to always cover unclaimed
  records + carry.
- **`rewards_fee_pool`** has **no** permissions and holds nothing while fees are
  disabled (v1). It exists so a future fee-enabled upgrade has a destination
  without a genesis/store rewrite.

## Permission constraints

- Only `rewards` can mint; `rewards_fee_pool` cannot.
- Neither account has `Burner` or `Staking` permissions.
- No staking/distribution/slashing/governance module accounts exist.

## Resolving module addresses

Module account addresses are derived from the module names (`rewards`,
`rewards_fee_pool`) via the auth module. The `module-balances` query reports both
balances and the denom:

```bash
twilightd rewards-query module-balances --node <rpc>
# { "denom": "utwlt", "rewards_balance": "...", "fee_pool_balance": "0" }
```
