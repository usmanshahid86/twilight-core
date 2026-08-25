package types

import "context"

// UpgradeScheduler is the whole of x/upgrade that x/coreslot is allowed to see.
//
// It is deliberately expressed in primitives rather than in the SDK's
// upgrade Plan. Two consequences follow, both intended:
//
//   - x/coreslot imports nothing from x/upgrade. The keeper DAG is unchanged, in
//     the same spirit as the economic-address validator being injected as a plain
//     value rather than as a keeper edge.
//   - the Plan is constructed in exactly one place, in app wiring, so the fields
//     this chain must never accept — a wall-clock upgrade time, an upgraded client
//     state — have no representation on any path a caller can reach. They are
//     unrepresentable rather than validated away.
type UpgradeScheduler interface {
	// ScheduleUpgrade records a plan to halt at height. Implementations reject a
	// height that is not in the future, and replace any plan already scheduled.
	ScheduleUpgrade(ctx context.Context, name string, height int64, info string) error
	// CancelUpgrade withdraws the scheduled plan, if any.
	CancelUpgrade(ctx context.Context) error
	// PendingUpgrade returns the scheduled plan's name, or "" when none is set.
	PendingUpgrade(ctx context.Context) (string, error)
}
