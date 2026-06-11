# x/rewards

`x/rewards` is Twilight's planned claim-based CoreSlot reward module. Emissions
are a subcomponent of rewards; staking, distribution, slashing, governance, and
proposer rewards are not dependencies.

Phase 1 contains only the versioned proto API and type/default/validation
scaffold. It does not include keeper state, lifecycle hooks, minting, claims
execution, fee collection, treasury execution, or app wiring.

Protocol accounting uses only `utwlt`, sourced from `app/params`.
