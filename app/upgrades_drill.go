//go:build upgradedrill

package app

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// The drill upgrade exists ONLY under the `upgradedrill` build tag.
//
// #131 needs two binaries that differ in exactly one respect — the compiled
// upgrade registry — because that difference is what the whole mechanism is
// built around. Producing them from one source revision with a build tag is what
// makes "same code, different registry" auditable: any other method (a branch, a
// patch, a second module) leaves open the question of what else changed.
//
// The released binary must never carry this. `go build ./cmd/twilightd` does not
// see this file at all, so drill-v2 is absent from production by construction
// rather than by convention.
func init() {
	Upgrades = append(Upgrades, Upgrade{
		Name:    DrillUpgradeName,
		Migrate: drillBumpKeyRotationDelay,
	})
}

// DrillUpgradeName is the name the #131 drill schedules.
const DrillUpgradeName = "drill-v2"

// drillBumpKeyRotationDelay moves CoreSlot's key-rotation delay from 1 to 2.
//
// # Why this migration, and why it is deliberately NOT idempotent
//
// The parameter is a disposable, observable marker: it is queryable from every
// node through `coreslot-query params`, so the drill proves the migration by
// reading COMMITTED CONSENSUS STATE on each validator rather than by trusting an
// in-process counter. Every node agreeing on the new value is the same evidence
// as an app-hash match, stated in a form an operator can read.
//
// The precondition is the point. A migration that simply assigns 2 would prove
// only that it ran AT LEAST once — it would pass identically if the upgrade were
// applied twice, which is the failure mode with the worst consequences for a real
// migration. Requiring the value to be 1 on entry means a second execution finds
// 2, returns an error, and halts the block. So "the chain is still producing
// blocks after a restart" is itself proof the migration did not run again.
//
// It touches nothing economic: no entitlement, no settlement cursor, no escrow,
// no validator set. The drill asserts those are unchanged across the boundary,
// which requires the migration to leave them alone.
func drillBumpKeyRotationDelay(ctx sdk.Context, k MigrationKeepers) error {
	params, err := k.CoreSlot.Params.Get(ctx)
	if err != nil {
		return fmt.Errorf("drill-v2: reading core slot params: %w", err)
	}
	if params.KeyRotationDelayBlocks != 1 {
		return fmt.Errorf(
			"drill-v2 requires key_rotation_delay_blocks == 1 on entry, found %d; "+
				"this migration is not idempotent and must run exactly once",
			params.KeyRotationDelayBlocks)
	}
	params.KeyRotationDelayBlocks = 2
	if err := params.Validate(); err != nil {
		return fmt.Errorf("drill-v2: migrated params are invalid: %w", err)
	}
	if err := k.CoreSlot.Params.Set(ctx, params); err != nil {
		return fmt.Errorf("drill-v2: persisting core slot params: %w", err)
	}
	return nil
}
