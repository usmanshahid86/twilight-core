package types

import "cosmossdk.io/collections"

const (
	ModuleName   = "mining"
	StoreKey     = ModuleName
	RouterKey    = ModuleName
	QuerierRoute = ModuleName
)

// Durable store-prefix ledger. A prefix byte is permanent: once assigned it is
// never recycled for different state, even if the collection it named is later
// removed or loses authority. Add new prefixes at the end and never renumber.
//
// This module owns its own store key, so the ledger starts at 0x01 and has no
// relationship to the prefixes used by x/coreslot or x/rewards.
var (
	// DistributionModeVersionsPrefix holds the immutable chain-wide
	// distribution-mode history keyed by the epoch a version becomes valid from.
	//
	// The record carries a validity INTERVAL rather than a single effective
	// epoch, unlike the two parameter histories below. That difference is
	// canonical and the shared resolution helpers are generic over the key rather
	// than over the record, so the interval is never flattened away.
	DistributionModeVersionsPrefix = collections.NewPrefix(0x01)
	// ScheduledDistributionModePrefix holds the single pending mode change keyed
	// by its effective epoch. POC 1 has no writer, so it is permanently empty.
	ScheduledDistributionModePrefix = collections.NewPrefix(0x02)

	// SelectionParamsVersionsPrefix holds the immutable global Selection-parameter
	// history keyed by effective epoch.
	SelectionParamsVersionsPrefix = collections.NewPrefix(0x03)
	// ScheduledSelectionParamsPrefix holds the single pending Selection-parameter
	// change. POC 1 has no writer.
	ScheduledSelectionParamsPrefix = collections.NewPrefix(0x04)

	// SettlementParamsVersionsPrefix holds the immutable settlement-validity
	// parameter history keyed by effective epoch. A target binds the version
	// effective two epochs before it, so nothing accepted afterwards can change
	// what that target may pay or how long it has to pay it.
	SettlementParamsVersionsPrefix = collections.NewPrefix(0x05)
	// ScheduledSettlementParamsPrefix holds the single pending settlement-parameter
	// change. POC 1 has no writer.
	ScheduledSettlementParamsPrefix = collections.NewPrefix(0x06)

	// SettlementClockKey holds the canonical monotonic settlement clock.
	//
	// It is NOT a block height. It advances once per block whose rewards-pause
	// state, effective at the beginning of that block, permits settlement release,
	// and does not advance at all while release is paused. Deadlines are measured
	// in this unit, which is why a pause freezes a settlement's remaining window
	// rather than consuming it.
	SettlementClockKey = collections.NewPrefix(0x07)
	// LastProcessedRewardEpochKey holds the materialization cursor: the newest
	// reward epoch whose complete settlement set has been created. It advances only
	// together with that complete set.
	LastProcessedRewardEpochKey = collections.NewPrefix(0x08)

	// SettlementEpochAnchorsPrefix holds one anchor per epoch that produced at
	// least one settlement. The anchor is the single canonical record of when that
	// epoch's settlements were created, rather than a clock duplicated into every
	// settlement row.
	SettlementEpochAnchorsPrefix = collections.NewPrefix(0x09)

	// SettlementsPrefix holds canonical settlement workflow rows keyed
	// (slot_id, epoch).
	//
	// The key order is (slot_id, epoch) and not the reverse. Materialization writes
	// one epoch at a time and would be equally served by either, but only this
	// order makes "every settlement of one Slot, ascending by epoch" a bounded
	// prefix range — which is the listing the settlement workflow and its operator
	// tooling actually need. A by-epoch listing has no consumer in the
	// architecture.
	SettlementsPrefix = collections.NewPrefix(0x0A)

	// OpenSettlementsBySlotPrefix holds DERIVED, rebuildable indexing state: the
	// subset of settlements that are still OPEN, keyed (slot_id, epoch).
	//
	// It exists so that listing a Slot's outstanding work costs the outstanding
	// rows rather than the Slot's whole finalized history, which grows for the life
	// of the chain. It carries no settlement content — only the key needed to reach
	// the canonical row — is absent from the genesis document, is rebuilt from
	// canonical rows on import, and is cross-checked against the row it points at
	// on every read. A divergence between the two is corruption, never an answer.
	OpenSettlementsBySlotPrefix = collections.NewPrefix(0x0B)
)
