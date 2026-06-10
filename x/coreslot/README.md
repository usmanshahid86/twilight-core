# x/coreslot

`x/coreslot` is the sole source of truth and sole CometBFT validator-update
emitter in Nyks v1.

The keeper stores slots, unique operator and consensus-key indexes, key
reservations, delayed key rotations, last-applied validators, and independent
reward weights. Messages express lifecycle intent. At EndBlock the keeper
applies due rotations in a cached context, derives the desired active set,
diffs it against the persisted last-applied set, rejects duplicates, sorts by
consensus-address bytes, persists the new set, and returns validator updates.

Statuses are `PENDING`, `ACTIVE`, `INACTIVE`, `SUSPENDED`, and terminal
`REMOVED`. Active removal is rejected; inactivate or suspend first.
