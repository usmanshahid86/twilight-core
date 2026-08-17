package keeper

import (
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"

	"github.com/twilight-project/twilight-core/internal/checked"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

// Canonical reward-configuration binding.
//
// RewardConfigVersion history is the sole authority for the economics an epoch's
// emission is computed under: the block subsidy that scales the mint, the share
// of that mint diverted to the treasury, and the destination it is diverted to.
//
// The binding rule is NOT "whatever is effective now" (§33):
//
//	RewardConfigForTarget(1)      = the genesis version
//	RewardConfigForTarget(2)      = the genesis version
//	RewardConfigForTarget(N >= 3) = the version effective at N-2
//
// so an update accepted during epoch E is effective at E+1 and first changes what
// a target pays at E+3. That gap is the whole point: by the time a target opens
// its preparation boundary, the economics it will settle under are already fixed
// and no later transaction can move them.
//
// # Relation to the epoch-geometry history
//
// This file deliberately mirrors epochhistory.go's structure — bounded record
// validation, a single adjacent-edge check, a predecessor seek — because the
// storage shape and the failure modes are the same. It does NOT mirror two things:
//
//   - there is no continuity equation. EpochConfigVersion carries an
//     effective_start_height and a length, so adjacent versions must agree about
//     where an epoch begins. RewardConfigVersion fixes no geometry and carries no
//     height, so the only relation across an edge is that version and effective
//     epoch both strictly increase. Inventing a continuity check here would be
//     checking a relation the record cannot express.
//   - promotion happens in EndBlock after monetary finalization succeeds, not at
//     BeginBlock. See promoteScheduledRewardConfig.

// validateRewardConfigRecord checks one history row against the key it was stored
// under and against the canonical shape every version must have.
//
// The economic admission bounds are re-checked on READ, not only on write. A
// stored subsidy of zero or a treasury share above the ratified ceiling cannot
// have been admitted by any current code path, so finding one means the row is
// not the row that was written — and this row is about to scale a mint.
func validateRewardConfigRecord(key uint64, version types.RewardConfigVersion) error {
	if version.EffectiveEpoch != key {
		return types.ErrInvalidState.Wrapf(
			"reward configuration version stored at effective epoch %d declares effective epoch %d",
			key, version.EffectiveEpoch)
	}
	return version.Validate()
}

// validateResolvedRewardConfig is the COMPLETE canonical rule, applied wherever a
// stored configuration is resolved for use rather than merely enumerated.
//
// It adds the treasury destination to the record's own shape. That check used to
// run only where a version was WRITTEN — at append and at genesis import — which
// left the read side one property short of the write side, and the missing
// property was the one that decides where value goes: a version carrying a
// positive emission share and an inadmissible destination passed resolution,
// scaled the mint, and was refused later at the transfer.
//
// Refused later still rolls back, because finalization runs in a cache. It is
// nonetheless the wrong ordering. The mint is the operation that creates the value
// the transfer then fails to move, so a configuration that cannot direct its own
// treasury share must be rejected BEFORE the first monetary step, not after it.
//
// This is configuration validation, not transfer-time validation, and it does not
// disturb §33.2: a computed treasury amount of zero still performs no send and
// still triggers no revalidation of the destination. What is checked here is that
// the configuration a positive share is read from names a destination at all.
func (k Keeper) validateResolvedRewardConfig(key uint64, version types.RewardConfigVersion) error {
	if err := validateRewardConfigRecord(key, version); err != nil {
		return err
	}
	return k.validateRewardConfigTreasury(
		fmt.Sprintf("reward configuration version %d at effective epoch %d",
			version.Version, version.EffectiveEpoch), version)
}

// requireRewardConfigAnchor refuses a history that has lost its permanent anchor.
//
// Version 1 effective at epoch 1 is protocol identity, not the earliest row that
// happens to be present: fresh genesis writes exactly it, and nothing removes it.
// The predecessor seek below cannot notice its absence on its own — it asks for
// the greatest effective epoch at or before a bound, and a later version answers
// that question perfectly well. A history whose anchor has gone is therefore not a
// shorter history, it is a history this module did not create, and resolving a
// target against its remains would compute money from it.
//
// One point read at a fixed key, so the check does not make resolution cost track
// chain age. Targets 1 and 2 do not need it: they read the anchor itself.
func (k Keeper) requireRewardConfigAnchor(ctx context.Context) error {
	_, err := k.GenesisRewardConfigVersion(ctx)
	return err
}

// rewardConfigPredecessor returns the history row immediately before the given
// effective epoch, reporting whether one exists.
//
// Held to the record's SHAPE only, not to the complete rule
// validateResolvedRewardConfig applies. That is deliberate: a predecessor is read
// to establish an ordering relation, never to compute money from, so requiring it
// to name a usable treasury destination would refuse a resolution over a
// configuration that is not the one being resolved. The row that governs is
// validated completely, where it is resolved.
func (k Keeper) rewardConfigPredecessor(
	ctx context.Context, effectiveEpoch uint64,
) (types.RewardConfigVersion, bool, error) {
	rng := new(collections.Range[uint64]).EndExclusive(effectiveEpoch).Descending()
	iter, err := k.RewardConfigVersions.Iterate(ctx, rng)
	if err != nil {
		return types.RewardConfigVersion{}, false, types.ErrInvalidState.Wrapf(
			"reward configuration history could not be read: %v", err)
	}
	defer iter.Close()
	if !iter.Valid() {
		return types.RewardConfigVersion{}, false, nil
	}
	key, err := iter.Key()
	if err != nil {
		return types.RewardConfigVersion{}, false, types.ErrInvalidState.Wrapf(
			"reward configuration history key could not be read: %v", err)
	}
	version, err := iter.Value()
	if err != nil {
		return types.RewardConfigVersion{}, false, types.ErrInvalidState.Wrapf(
			"reward configuration version at effective epoch %d could not be read: %v", key, err)
	}
	if err := validateRewardConfigRecord(key, version); err != nil {
		return types.RewardConfigVersion{}, false, err
	}
	return version, true, nil
}

// validateAdjacentRewardConfigEdge checks a version against the one immediately
// before it.
//
// # The ratified relation
//
// version is a CONTIGUOUS protocol sequence number: the genesis anchor is 1 and
// every later version is exactly its predecessor's plus one. Effective epochs
// merely increase; version numbers count.
//
// Strict increase was the weaker rule this used to enforce, and the weakness was
// not cosmetic. Under it a history could hold 1, then 3, and a query for version 2
// had no way to tell "never accepted" from "the record is gone" — both look like
// a number the history does not contain. Contiguity is what makes those two
// distinguishable, so it is enforced rather than assumed.
//
// Every record establishes this against its predecessor when it is appended, so a
// history built only through the append path is inductively contiguous; checking
// one edge on read is what detects a row that changed underneath that induction,
// without making validation cost grow with chain age.
func (k Keeper) validateAdjacentRewardConfigEdge(ctx context.Context, version types.RewardConfigVersion) error {
	predecessor, found, err := k.rewardConfigPredecessor(ctx, version.EffectiveEpoch)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	successor, err := checked.AddUint64(predecessor.Version, 1)
	if err != nil {
		return types.ErrInvalidState.Wrapf(
			"reward configuration version %d at effective epoch %d has no representable successor",
			predecessor.Version, predecessor.EffectiveEpoch)
	}
	if version.Version != successor {
		return types.ErrInvalidState.Wrapf(
			"reward configuration version %d at effective epoch %d does not immediately follow version %d at effective epoch %d; "+
				"canonical versions are contiguous",
			version.Version, version.EffectiveEpoch, predecessor.Version, predecessor.EffectiveEpoch)
	}
	return nil
}

// rewardConfigVersionFor returns the version effective at the given epoch.
//
// This resolves an EFFECTIVE epoch, not a target. Callers that want the
// configuration governing a target must go through RewardConfigForTarget, which
// applies the N-2 binding and the bootstrap rule first.
func (k Keeper) rewardConfigVersionFor(ctx context.Context, epoch uint64) (types.RewardConfigVersion, error) {
	if epoch == 0 {
		return types.RewardConfigVersion{}, types.ErrInvalidState.Wrap("epoch numbers start at 1")
	}
	// The permanent anchor, before the seek that would otherwise resolve happily
	// without it.
	if err := k.requireRewardConfigAnchor(ctx); err != nil {
		return types.RewardConfigVersion{}, err
	}

	rng := new(collections.Range[uint64]).EndInclusive(epoch).Descending()
	iter, err := k.RewardConfigVersions.Iterate(ctx, rng)
	if err != nil {
		return types.RewardConfigVersion{}, types.ErrInvalidState.Wrapf(
			"reward configuration history could not be read: %v", err)
	}
	defer iter.Close()

	if !iter.Valid() {
		return types.RewardConfigVersion{}, types.ErrRewardConfigNotFound.Wrapf(
			"no reward configuration version is effective at or before epoch %d", epoch)
	}
	key, err := iter.Key()
	if err != nil {
		return types.RewardConfigVersion{}, types.ErrInvalidState.Wrapf(
			"reward configuration history key could not be read: %v", err)
	}
	version, err := iter.Value()
	if err != nil {
		return types.RewardConfigVersion{}, types.ErrInvalidState.Wrapf(
			"reward configuration version at effective epoch %d could not be read: %v", key, err)
	}
	if err := k.validateResolvedRewardConfig(key, version); err != nil {
		return types.RewardConfigVersion{}, err
	}
	if err := k.validateAdjacentRewardConfigEdge(ctx, version); err != nil {
		return types.RewardConfigVersion{}, err
	}
	return version, nil
}

// GenesisRewardConfigVersion returns the initial reward-configuration anchor.
//
// It reads the row stored at effective epoch 1 directly, and requires it to be
// version 1. That is stricter than "the earliest row": the anchor is permanent
// protocol identity, and a history whose first entry is not version 1 effective
// at epoch 1 is not a history this module created.
func (k Keeper) GenesisRewardConfigVersion(ctx context.Context) (types.RewardConfigVersion, error) {
	version, err := k.RewardConfigVersions.Get(ctx, 1)
	if errors.Is(err, collections.ErrNotFound) {
		return types.RewardConfigVersion{}, types.ErrRewardConfigNotFound.Wrap(
			"the initial reward configuration version effective at epoch 1 is absent")
	}
	if err != nil {
		return types.RewardConfigVersion{}, types.ErrInvalidState.Wrapf(
			"the initial reward configuration version could not be read: %v", err)
	}
	if err := k.validateResolvedRewardConfig(1, version); err != nil {
		return types.RewardConfigVersion{}, err
	}
	if version.Version != 1 {
		return types.RewardConfigVersion{}, types.ErrInvalidState.Wrapf(
			"the initial reward configuration must be version 1 effective at epoch 1, found version %d",
			version.Version)
	}
	return version, nil
}

// RewardConfigForTarget returns the reward configuration governing target epoch N
// (§33.1).
//
// # Why the bootstrap branch returns before the subtraction
//
// Epoch numbers are unsigned. For targets 1 and 2 there is no N-2 boundary inside
// chain history at all, and the bootstrap decision is made HERE, structurally
// ahead of any arithmetic, rather than by guarding a subtraction further down.
// The distinction matters because the two failure modes are not equally visible:
//
//   - a clamp of N-2 to epoch 1 returns the right answer for as long as the
//     history holds one version, then silently keeps returning the genesis version
//     after it should have stopped. It encodes a pre-genesis epoch that does not
//     exist.
//   - an unguarded subtraction underflows to a huge epoch, and a lookup for the
//     greatest effective_epoch <= 2^64-1 resolves the LATEST version — the exact
//     opposite of the intended answer, and a different amount of money.
//
// Placing the return above the subtraction means neither is reachable by a later
// edit that reorders a guard.
func (k Keeper) RewardConfigForTarget(ctx context.Context, target uint64) (types.RewardConfigVersion, error) {
	if target == 0 {
		return types.RewardConfigVersion{}, types.ErrInvalidState.Wrap("epoch numbers start at 1")
	}
	if target <= 2 {
		return k.GenesisRewardConfigVersion(ctx)
	}
	bindingEpoch, err := checked.SubUint64(target, 2)
	if err != nil {
		return types.RewardConfigVersion{}, types.ErrInvalidState.Wrapf(
			"target epoch %d has no representable N-2 binding boundary: %v", target, err)
	}
	return k.rewardConfigVersionFor(ctx, bindingEpoch)
}

// setRewardConfigVersionIndex records the derived version -> effective epoch
// mapping for one history row.
//
// Write-once. A version number that already has an entry makes "the record for
// version N" ambiguous, which is not something the lookup can answer, so it is
// refused rather than overwritten. That is a rule about the DERIVED index's own
// well-formedness and takes no position on how the history collection treats
// entries duplicated under its own key, which is a separate open question.
func (k Keeper) setRewardConfigVersionIndex(ctx context.Context, version types.RewardConfigVersion) error {
	existing, err := k.RewardConfigVersionIndex.Get(ctx, version.Version)
	switch {
	case err == nil:
		return types.ErrInvalidState.Wrapf(
			"reward configuration version %d is already indexed at effective epoch %d and cannot also be at epoch %d",
			version.Version, existing, version.EffectiveEpoch)
	case !errors.Is(err, collections.ErrNotFound):
		return types.ErrInvalidState.Wrapf(
			"reward configuration version index for version %d could not be read: %v", version.Version, err)
	}
	return k.RewardConfigVersionIndex.Set(ctx, version.Version, version.EffectiveEpoch)
}

// RewardConfigVersionByNumber resolves a history entry by its version number,
// reporting whether one exists.
//
// # Why this goes through an index rather than a walk
//
// The history is keyed by effective epoch, so a bare version number has no direct
// key. The obvious substitute — walk ascending and stop once the rows pass the
// number asked for — is wrong twice over, and the second way is the serious one:
//
//   - a version that is absent from a long history is only discovered after
//     visiting every row below it, so the cost of a lookup tracks chain age.
//   - the stopping rule ASSUMES the ordering it is supposed to be reading. Given a
//     history holding v1, then v9, then v3, a walk asking for version 3 sees v9,
//     concludes it has gone past, and reports NotFound — turning corruption into
//     an ordinary "no such version" answer, which is exactly the confusion every
//     other read path in this module is built to avoid.
//
// A bounded, constant number of canonical and index point reads instead, plus the
// constant adjacent-edge validation every resolution performs. None of it depends
// on how many configuration changes the chain has accepted.
//
// # How absence is decided, and why the index cannot decide it
//
// Versions are a CONTIGUOUS sequence, so the history holds exactly 1..latest. That
// makes the question answerable from the canonical history alone:
//
//	number > latest        -> the version was never assigned. Absence.
//	number <= latest       -> the version exists in canonical history.
//
// The derived index is consulted only AFTER that decision, and only to locate the
// row. A missing index entry for an in-range version is therefore not an answer,
// it is a hole in derived state — reported as corruption rather than as "no such
// version", which is what it would have looked like if the index were allowed to
// classify absence for itself.
//
// The ordering matters: consult the authority for the question it can answer, and
// the accelerator only for the one it cannot.
//
// # The index is checked, not trusted
//
// Both directions are verified against the row itself — the row must declare the
// version that was asked for AND the effective epoch the index pointed at. An
// index that disagrees with the history is corruption and fails closed; it can
// never be the reason an answer differs, only the reason one is found quickly.
//
// This is a QUERY seam. Nothing on a block path may call it: the epoch's own
// binding is what money is computed from, and resolving that never needs a
// version-number search.
func (k Keeper) RewardConfigVersionByNumber(
	ctx context.Context, number uint64,
) (types.RewardConfigVersion, bool, error) {
	if number == 0 {
		return types.RewardConfigVersion{}, false, types.ErrInvalidState.Wrap(
			"reward configuration version numbers start at 1")
	}
	if err := k.requireRewardConfigAnchor(ctx); err != nil {
		return types.RewardConfigVersion{}, false, err
	}
	// The authority decides absence. latestRewardConfigVersion is a single reverse
	// seek to the newest row plus its own edge check, so this is constant work and
	// not a walk.
	latest, err := k.latestRewardConfigVersion(ctx)
	if err != nil {
		return types.RewardConfigVersion{}, false, err
	}
	if number > latest.Version {
		// Never assigned. The only genuine absence there is.
		return types.RewardConfigVersion{}, false, nil
	}

	effectiveEpoch, err := k.RewardConfigVersionIndex.Get(ctx, number)
	if errors.Is(err, collections.ErrNotFound) {
		// In range, so canonical history HAS this version — the index has lost it.
		// Derived state that cannot locate a record the authority says exists is
		// corruption, and reporting it as absence would tell a client the chain
		// never accepted a configuration it is still governed by.
		return types.RewardConfigVersion{}, false, types.ErrInvalidState.Wrapf(
			"reward configuration version %d is within the canonical range 1..%d but has no index entry",
			number, latest.Version)
	}
	if err != nil {
		return types.RewardConfigVersion{}, false, types.ErrInvalidState.Wrapf(
			"reward configuration version index for version %d could not be read: %v", number, err)
	}

	version, err := k.RewardConfigVersions.Get(ctx, effectiveEpoch)
	if errors.Is(err, collections.ErrNotFound) {
		// The index names a row the history does not have. Not absence — the two
		// disagree, and one of them is wrong.
		return types.RewardConfigVersion{}, false, types.ErrInvalidState.Wrapf(
			"reward configuration version %d is indexed at effective epoch %d, where the canonical history has no record",
			number, effectiveEpoch)
	}
	if err != nil {
		return types.RewardConfigVersion{}, false, types.ErrInvalidState.Wrapf(
			"reward configuration version at effective epoch %d could not be read: %v", effectiveEpoch, err)
	}
	if err := k.validateResolvedRewardConfig(effectiveEpoch, version); err != nil {
		return types.RewardConfigVersion{}, false, err
	}
	if version.Version != number {
		return types.RewardConfigVersion{}, false, types.ErrInvalidState.Wrapf(
			"reward configuration version %d is indexed at effective epoch %d, where the canonical history holds version %d",
			number, effectiveEpoch, version.Version)
	}
	if err := k.validateAdjacentRewardConfigEdge(ctx, version); err != nil {
		return types.RewardConfigVersion{}, false, err
	}
	return version, true, nil
}

// RewardConfigVersionAtEffectiveEpoch reads the history entry stored at one
// effective epoch, reporting whether one exists.
//
// An EXACT key read, not the predecessor seek rewardConfigVersionFor performs.
// The two answer different questions and the difference matters to a caller: this
// says "a configuration became effective at epoch N", while the seek says "epoch N
// is governed by whatever became effective at or before it". A query that
// conflated them would report a version as effective at every epoch after it.
func (k Keeper) RewardConfigVersionAtEffectiveEpoch(
	ctx context.Context, effectiveEpoch uint64,
) (types.RewardConfigVersion, bool, error) {
	if effectiveEpoch == 0 {
		return types.RewardConfigVersion{}, false, types.ErrInvalidState.Wrap("epoch numbers start at 1")
	}
	if err := k.requireRewardConfigAnchor(ctx); err != nil {
		return types.RewardConfigVersion{}, false, err
	}
	version, err := k.RewardConfigVersions.Get(ctx, effectiveEpoch)
	if errors.Is(err, collections.ErrNotFound) {
		return types.RewardConfigVersion{}, false, nil
	}
	if err != nil {
		return types.RewardConfigVersion{}, false, types.ErrInvalidState.Wrapf(
			"reward configuration version at effective epoch %d could not be read: %v", effectiveEpoch, err)
	}
	if err := k.validateResolvedRewardConfig(effectiveEpoch, version); err != nil {
		return types.RewardConfigVersion{}, false, err
	}
	if err := k.validateAdjacentRewardConfigEdge(ctx, version); err != nil {
		return types.RewardConfigVersion{}, false, err
	}
	return version, true, nil
}

// latestRewardConfigVersion returns the newest history entry, which is also the
// highest version number.
//
// The last row is not trusted on the strength of being last: its key is read and
// checked against the record, the record is validated, and so is the edge behind
// it. This value is what the next promoted version derives its identity FROM, so
// building on a malformed latest row would launder the corruption into a record
// that afterwards looks canonical.
func (k Keeper) latestRewardConfigVersion(ctx context.Context) (types.RewardConfigVersion, error) {
	iter, err := k.RewardConfigVersions.Iterate(ctx, new(collections.Range[uint64]).Descending())
	if err != nil {
		return types.RewardConfigVersion{}, types.ErrInvalidState.Wrapf(
			"reward configuration history could not be read: %v", err)
	}
	defer iter.Close()
	if !iter.Valid() {
		return types.RewardConfigVersion{}, types.ErrRewardConfigNotFound.Wrap(
			"reward configuration history is empty")
	}
	key, err := iter.Key()
	if err != nil {
		return types.RewardConfigVersion{}, types.ErrInvalidState.Wrapf(
			"latest reward configuration history key could not be read: %v", err)
	}
	version, err := iter.Value()
	if err != nil {
		return types.RewardConfigVersion{}, types.ErrInvalidState.Wrapf(
			"latest reward configuration version could not be read: %v", err)
	}
	if err := k.validateResolvedRewardConfig(key, version); err != nil {
		return types.RewardConfigVersion{}, err
	}
	if err := k.validateAdjacentRewardConfigEdge(ctx, version); err != nil {
		return types.RewardConfigVersion{}, err
	}
	return version, nil
}

// appendRewardConfigVersion writes one immutable history entry.
//
// Every property the read path later relies on is established here: the record's
// own shape, its economic admission bounds, the treasury destination when the
// share can direct value, and the ordering against the history it extends.
// Strictly increasing effective epoch also subsumes write-once — a version at an
// effective epoch that already has one would rewrite history rather than extend
// it, and is refused rather than overwritten.
func (k Keeper) appendRewardConfigVersion(ctx context.Context, version types.RewardConfigVersion) error {
	if err := validateRewardConfigRecord(version.EffectiveEpoch, version); err != nil {
		return err
	}
	if err := k.validateRewardConfigTreasury("reward configuration version", version); err != nil {
		return err
	}
	latest, err := k.latestRewardConfigVersion(ctx)
	if err != nil {
		return err
	}
	if version.EffectiveEpoch <= latest.EffectiveEpoch {
		return types.ErrInvalidState.Wrapf(
			"reward configuration version %d would take effect at epoch %d, at or before the latest version %d effective at epoch %d",
			version.Version, version.EffectiveEpoch, latest.Version, latest.EffectiveEpoch)
	}
	// The ratified contiguity rule, at the one place canonical history is created
	// at runtime. Checked arithmetic, so an exhausted version space fails closed
	// rather than wrapping to a number that would look like a fresh anchor.
	successor, err := checked.AddUint64(latest.Version, 1)
	if err != nil {
		return types.ErrInvalidState.Wrap("reward configuration version space is exhausted")
	}
	if version.Version != successor {
		return types.ErrInvalidState.Wrapf(
			"reward configuration version %d does not immediately follow the latest version %d; "+
				"canonical versions are contiguous",
			version.Version, latest.Version)
	}
	if err := k.RewardConfigVersions.Set(ctx, version.EffectiveEpoch, version); err != nil {
		return err
	}
	// The derived index moves with the row it describes. Both writes happen inside
	// the EndBlock cache promotion runs in, so a failure here discards the history
	// row as well — the two cannot commit apart and leave the index describing a
	// history that does not exist, or a history the index cannot reach.
	return k.setRewardConfigVersionIndex(ctx, version)
}

// ValidateScheduledRewardConfigRecord checks one schedule row against the key it
// was stored under and against the canonical shape a schedule entry must have.
//
// A scheduled entry becomes an immutable history version unchanged the moment its
// epoch opens, so it is held to the same admission bounds as history itself: an
// inadmissible subsidy or treasury share is refused where it is read, not
// silently promoted at the boundary.
func ValidateScheduledRewardConfigRecord(key uint64, scheduled types.ScheduledRewardConfig) error {
	if scheduled.EffectiveEpoch != key {
		return types.ErrInvalidState.Wrapf(
			"scheduled reward configuration stored at epoch %d declares effective epoch %d",
			key, scheduled.EffectiveEpoch)
	}
	return scheduled.Validate()
}

// ScheduledRewardConfigFor returns the pending reward configuration effective at
// the given epoch, reporting whether one exists.
//
// Absence is ordinary: most epochs schedule nothing. Anything else is corruption.
func (k Keeper) ScheduledRewardConfigFor(
	ctx context.Context, effectiveEpoch uint64,
) (types.ScheduledRewardConfig, bool, error) {
	scheduled, err := k.ScheduledRewardConfigs.Get(ctx, effectiveEpoch)
	if errors.Is(err, collections.ErrNotFound) {
		return types.ScheduledRewardConfig{}, false, nil
	}
	if err != nil {
		return types.ScheduledRewardConfig{}, false, types.ErrInvalidState.Wrapf(
			"scheduled reward configuration at epoch %d could not be read: %v", effectiveEpoch, err)
	}
	if err := ValidateScheduledRewardConfigRecord(effectiveEpoch, scheduled); err != nil {
		return types.ScheduledRewardConfig{}, false, err
	}
	return scheduled, true, nil
}

// promoteScheduledRewardConfig turns a configuration scheduled for the epoch
// after the closing one into an immutable history version.
//
// # Where this runs, and why not BeginBlock
//
// The epoch-geometry schedule is consumed at the first BeginBlock of the epoch it
// becomes effective at, because geometry has to be known before that block is
// attributed to an epoch. Reward configuration is the opposite: it is bound two
// epochs ahead of any target it can affect, so nothing needs it early — and
// promoting it early would be actively wrong. §94 places promotion in the closing
// epoch's EndBlock, after that epoch's monetary transition has succeeded, so a
// configuration can never become effective beside a mint that failed.
//
// This function is therefore called from EndBlock with the SAME cache the
// finalization ran in. Ordering gives "only after success"; sharing the cache
// gives the converse — a promotion failure discards the mint rather than leaving
// a committed epoch beside an unconsumed schedule.
//
// # The single-entry rule
//
// §82 admits at most one scheduled reward configuration, keyed exactly at
// current_epoch + 1. Any other key is invalid state rather than a queue entry, so
// the whole collection is checked here rather than only the key being consumed.
// Doing that is bounded work: the collection holds at most one row.
func (k Keeper) promoteScheduledRewardConfig(ctx context.Context, closingEpoch uint64) error {
	effectiveEpoch, err := checked.AddUint64(closingEpoch, 1)
	if err != nil {
		return types.ErrInvalidState.Wrap("reward configuration scheduling epoch is exhausted")
	}
	if err := k.assertSingleScheduledRewardConfig(ctx, effectiveEpoch); err != nil {
		return err
	}
	scheduled, found, err := k.ScheduledRewardConfigFor(ctx, effectiveEpoch)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}

	latest, err := k.latestRewardConfigVersion(ctx)
	if err != nil {
		return err
	}
	nextVersion, err := checked.AddUint64(latest.Version, 1)
	if err != nil {
		return types.ErrInvalidState.Wrap("reward configuration version space is exhausted")
	}
	if err := k.appendRewardConfigVersion(ctx, types.RewardConfigVersion{
		Version:                  nextVersion,
		EffectiveEpoch:           effectiveEpoch,
		InitialBlockSubsidy:      scheduled.InitialBlockSubsidy,
		EmissionTreasuryShareBps: scheduled.EmissionTreasuryShareBps,
		TreasuryAddress:          scheduled.TreasuryAddress,
	}); err != nil {
		return err
	}
	if err := k.ScheduledRewardConfigs.Remove(ctx, effectiveEpoch); err != nil {
		return types.ErrInvalidState.Wrapf(
			"scheduled reward configuration at epoch %d could not be consumed: %v", effectiveEpoch, err)
	}
	return nil
}

// assertSingleScheduledRewardConfig refuses a schedule holding anything other
// than the one admissible entry.
//
// Walking the collection is bounded by the rule being enforced: a valid schedule
// holds zero or one row, and an invalid one halts the block on the first
// unexpected key rather than being walked to the end.
func (k Keeper) assertSingleScheduledRewardConfig(ctx context.Context, effectiveEpoch uint64) error {
	return k.ScheduledRewardConfigs.Walk(ctx, nil, func(key uint64, _ types.ScheduledRewardConfig) (bool, error) {
		if key != effectiveEpoch {
			return true, types.ErrInvalidState.Wrapf(
				"a reward configuration is scheduled at epoch %d, but the only admissible scheduling key is epoch %d",
				key, effectiveEpoch)
		}
		return false, nil
	})
}
