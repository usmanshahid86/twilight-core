package keeper_test

// Conformance of the reward arithmetic against the normative reward
// consensus-vector pack.
//
// ---------------------------------------------------------------------------
// COVERAGE BOUNDARY — read this before treating a green run as broad assurance.
//
// Passing these vectors certifies exactly three things:
//
//   - emission arithmetic — per-block subsidy under supply-threshold halving,
//     and the epoch emission accumulated from it;
//   - allocation arithmetic — uniform allocation by active blocks, the floor
//     behavior, and the carry-forward remainder;
//   - reward-pool arithmetic — pool = minted emission - treasury + carry in.
//
// It does NOT certify:
//
//   - historical RewardConfigVersion resolution — which configuration governs a
//     given epoch. These vectors carry numbers, not version histories, so no
//     case here can distinguish a correct resolution rule from an incorrect one.
//   - treasury or bank transfer side effects — whether a transfer occurred, how
//     many times, and what accounts it touched or created. These vectors assert
//     computed amounts; a side effect is invisible to them, and an amount of
//     zero is consistent both with a transfer that was skipped and with one that
//     was performed.
//
// Both of those need state-transition and effects fixtures against a keeper with
// a mocked bank, and neither exists yet. A green run here is arithmetic
// conformance and nothing wider.
// ---------------------------------------------------------------------------

import (
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/twilight-project/twilight-core/internal/consensusvectors"
	"github.com/twilight-project/twilight-core/x/rewards/keeper"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

func mustAmount(t *testing.T, decimal string) math.Int {
	t.Helper()
	value, ok := math.NewIntFromString(decimal)
	require.Truef(t, ok, "amount %q is not a decimal integer", decimal)
	return value
}

// allocationRows builds the keeper inputs for an activity profile. Slot IDs are
// assigned by position so the vector's entitlement order is the row order.
func allocationRows(blocksActive []consensusvectors.U64) ([]types.SlotActiveBlocks, map[uint64]keeper.SlotRewardSnapshot) {
	rows := make([]types.SlotActiveBlocks, 0, len(blocksActive))
	snapshots := make(map[uint64]keeper.SlotRewardSnapshot, len(blocksActive))
	for i, blocks := range blocksActive {
		slotID := uint64(i + 1)
		rows = append(rows, types.SlotActiveBlocks{SlotId: slotID, BlocksActive: blocks.Uint64()})
		snapshots[slotID] = keeper.SlotRewardSnapshot{
			SlotID:          slotID,
			OperatorAddress: mustAddress(byte(2 * slotID)),
			PayoutAddress:   mustAddress(byte(2*slotID + 1)),
		}
	}
	return rows, snapshots
}

func TestRewardPackConformance(t *testing.T) {
	pack, err := consensusvectors.LoadRewardPack()
	require.NoError(t, err, "load reward pack")

	ledger := &consensusvectors.CaseLedger{}
	const packName = consensusvectors.RewardPackFilename

	t.Run("emission_vectors", func(t *testing.T) {
		runEmissionVectors(t, pack, ledger)
	})
	t.Run("allocation_vectors", func(t *testing.T) {
		runAllocationVectors(t, pack, ledger)
	})
	t.Run("pool_vectors", func(t *testing.T) {
		runPoolVectors(t, pack, ledger)
	})
	t.Run("required_assertions", func(t *testing.T) {
		runRequiredAssertions(t, pack, ledger)
	})
	t.Run("negative_discriminators", func(t *testing.T) {
		runNegativeDiscriminators(t, pack, ledger)
	})

	t.Run("coverage_ledger", func(t *testing.T) {
		require.NoError(t, ledger.ValidateNoDeferredExecuted())

		sections := []struct {
			section string
			want    int
		}{
			{"emission_vectors", consensusvectors.ExpectedEmissionVectors},
			{"allocation_vectors", consensusvectors.ExpectedAllocationVectors},
			{"pool_vectors", consensusvectors.ExpectedPoolVectors},
			{"required_assertions", consensusvectors.ExpectedRequiredAssertions},
			{"negative_discriminators", consensusvectors.ExpectedNegativeDiscriminators},
		}
		total := 0
		for _, section := range sections {
			require.Equalf(t, section.want, ledger.Count(packName, section.section),
				"executed %s cases", section.section)
			total += section.want
		}
		require.Equal(t, total, ledger.Total(), "total executed reward-pack cases")
	})
}

func runEmissionVectors(t *testing.T, pack consensusvectors.RewardPack, ledger *consensusvectors.CaseLedger) {
	for _, v := range pack.EmissionVectors {
		t.Run(v.Name, func(t *testing.T) {
			cumulativeBefore := mustAmount(t, v.CumulativeBefore)
			maxSupply := mustAmount(t, v.MaxSupply)
			initialSubsidy := mustAmount(t, v.InitialBlockSubsid)
			blocks := v.RewardEnabledBlock.Uint64()

			// The pack publishes the per-block subsidy schedule, so the halving
			// curve is pinned block by block and not only through its total. A
			// wrong tier that happens to sum correctly would still fail here.
			require.Lenf(t, v.PerBlockSubsidies, int(blocks),
				"per_block_subsidies length must equal reward_enabled_blocks")
			running := cumulativeBefore
			for i, wantHex := range v.PerBlockSubsidies {
				subsidy, err := keeper.NextBlockSubsidy(running, maxSupply, initialSubsidy)
				require.NoErrorf(t, err, "NextBlockSubsidy at block %d", i)
				require.Equalf(t, mustAmount(t, wantHex).String(), subsidy.String(),
					"per-block subsidy at block %d", i)
				running = running.Add(subsidy)
			}

			emission, cumulativeAfter, err := keeper.ComputeEpochEmission(
				cumulativeBefore, blocks, maxSupply, initialSubsidy,
				types.HalvingMode_HALVING_MODE_SUPPLY_THRESHOLD,
			)
			require.NoError(t, err, "ComputeEpochEmission")
			require.Equal(t, mustAmount(t, v.MintedEmission).String(), emission.String(), "minted emission")
			require.Equal(t, mustAmount(t, v.CumulativeAfter).String(), cumulativeAfter.String(), "cumulative after")

			// The block-by-block walk and the epoch function must agree.
			require.Equal(t, running.String(), cumulativeAfter.String(),
				"per-block walk disagrees with ComputeEpochEmission")

			ledger.Record(consensusvectors.RewardPackFilename, "emission_vectors", v.Name)
		})
	}
}

func runAllocationVectors(t *testing.T, pack consensusvectors.RewardPack, ledger *consensusvectors.CaseLedger) {
	for _, v := range pack.AllocationVectors {
		t.Run(v.Name, func(t *testing.T) {
			pool := mustAmount(t, v.Pool)
			rows, snapshots := allocationRows(v.BlocksActive)

			entitlements, allocated, carryOut, _, err := keeper.AllocateSlotEntitlements(
				1, pool, rows, snapshots, 1, 1)
			require.NoError(t, err, "AllocateSlotEntitlements")

			// Entitlements are stated per input Slot, including Slots that receive
			// nothing; the keeper omits zero-amount rows, so the comparison is made
			// against a per-Slot map rather than against the returned row order.
			byslot := make(map[uint64]math.Int, len(entitlements))
			for _, entitlement := range entitlements {
				byslot[entitlement.SlotId] = mustAmount(t, entitlement.EntitlementAmount)
			}
			for i, wantDecimal := range v.Entitlements {
				slotID := uint64(i + 1)
				want := mustAmount(t, wantDecimal)
				got, present := byslot[slotID]
				if !present {
					got = math.ZeroInt()
				}
				require.Equalf(t, want.String(), got.String(), "entitlement for slot %d", slotID)
			}

			require.Equal(t, mustAmount(t, v.Allocated).String(), allocated.String(), "allocated")
			require.Equal(t, mustAmount(t, v.CarryOut).String(), carryOut.String(), "carry out")

			ledger.Record(consensusvectors.RewardPackFilename, "allocation_vectors", v.Name)
		})
	}
}

func runPoolVectors(t *testing.T, pack consensusvectors.RewardPack, ledger *consensusvectors.CaseLedger) {
	for _, v := range pack.PoolVectors {
		t.Run(v.Name, func(t *testing.T) {
			mintedEmission := mustAmount(t, v.MintedEmission)
			carryIn := mustAmount(t, v.CarryIn)

			// The treasury amount is recomputed from the share rather than taken
			// from the vector, so the split itself is under test.
			treasury, err := keeper.ComputeEmissionTreasuryAmount(mintedEmission, v.TreasuryShareBps.Uint64())
			require.NoError(t, err, "ComputeEmissionTreasuryAmount")
			require.Equal(t, mustAmount(t, v.Treasury).String(), treasury.String(), "treasury")

			pool, err := keeper.ComputeRewardPoolV2(mintedEmission, treasury, carryIn)
			require.NoError(t, err, "ComputeRewardPoolV2")
			require.Equal(t, mustAmount(t, v.Pool).String(), pool.String(), "pool")

			ledger.Record(consensusvectors.RewardPackFilename, "pool_vectors", v.Name)
		})
	}
}

// runRequiredAssertions checks each stated invariant across the vectors it
// governs, rather than restating it as a comment.
func runRequiredAssertions(t *testing.T, pack consensusvectors.RewardPack, ledger *consensusvectors.CaseLedger) {
	const section = "required_assertions"
	packName := consensusvectors.RewardPackFilename

	// The assertions are matched by their exact pack text, so a reworded or added
	// invariant fails rather than passing unchecked.
	handled := map[string]func(t *testing.T){
		"allocated <= pool before carry definition": func(t *testing.T) {
			for _, v := range pack.AllocationVectors {
				pool := mustAmount(t, v.Pool)
				rows, snapshots := allocationRows(v.BlocksActive)
				_, allocated, _, _, err := keeper.AllocateSlotEntitlements(1, pool, rows, snapshots, 1, 1)
				require.NoError(t, err)
				require.Truef(t, allocated.LTE(pool), "%s: allocated %s exceeds pool %s", v.Name, allocated, pool)
			}
		},
		"carry = pool - allocated": func(t *testing.T) {
			for _, v := range pack.AllocationVectors {
				pool := mustAmount(t, v.Pool)
				rows, snapshots := allocationRows(v.BlocksActive)
				_, allocated, carryOut, _, err := keeper.AllocateSlotEntitlements(1, pool, rows, snapshots, 1, 1)
				require.NoError(t, err)
				require.Equalf(t, pool.Sub(allocated).String(), carryOut.String(), "%s", v.Name)
			}
		},
		"carry >= 0": func(t *testing.T) {
			for _, v := range pack.AllocationVectors {
				pool := mustAmount(t, v.Pool)
				rows, snapshots := allocationRows(v.BlocksActive)
				_, _, carryOut, _, err := keeper.AllocateSlotEntitlements(1, pool, rows, snapshots, 1, 1)
				require.NoError(t, err)
				require.Falsef(t, carryOut.IsNegative(), "%s: carry %s is negative", v.Name, carryOut)
			}
		},
		"when n_pos>0 carry <= n_pos-1": func(t *testing.T) {
			// The bound follows from flooring once per participating Slot: each
			// division discards strictly less than one unit.
			for _, v := range pack.AllocationVectors {
				if v.NPos.Value() == 0 {
					continue
				}
				pool := mustAmount(t, v.Pool)
				rows, snapshots := allocationRows(v.BlocksActive)
				_, _, carryOut, _, err := keeper.AllocateSlotEntitlements(1, pool, rows, snapshots, 1, 1)
				require.NoError(t, err)
				require.Truef(t, carryOut.LTE(math.NewInt(int64(v.NPos.Value()-1))),
					"%s: carry %s exceeds n_pos-1 = %d", v.Name, carryOut, v.NPos.Value()-1)
			}
		},
		"cumulative_after <= max_supply": func(t *testing.T) {
			for _, v := range pack.EmissionVectors {
				_, cumulativeAfter, err := keeper.ComputeEpochEmission(
					mustAmount(t, v.CumulativeBefore), v.RewardEnabledBlock.Uint64(),
					mustAmount(t, v.MaxSupply), mustAmount(t, v.InitialBlockSubsid),
					types.HalvingMode_HALVING_MODE_SUPPLY_THRESHOLD,
				)
				require.NoError(t, err)
				maxSupply := mustAmount(t, v.MaxSupply)
				require.Truef(t, cumulativeAfter.LTE(maxSupply),
					"%s: cumulative after %s exceeds max supply %s", v.Name, cumulativeAfter, maxSupply)
			}
		},
		"carry_in(E)=carry_out(E-1)": func(t *testing.T) {
			// Chained through the pack's own numbers: the treasury-and-carry pool
			// vector produces pool 900007, which is exactly the pool of the
			// unequal-active-blocks allocation vector, whose carry out is then the
			// carry in of the following epoch.
			poolVector := poolVectorNamed(t, pack, "treasury-and-carry")
			allocVector := allocationVectorNamed(t, pack, "unequal-active-blocks")

			mintedEmission := mustAmount(t, poolVector.MintedEmission)
			treasury, err := keeper.ComputeEmissionTreasuryAmount(mintedEmission, poolVector.TreasuryShareBps.Uint64())
			require.NoError(t, err)
			poolE, err := keeper.ComputeRewardPoolV2(mintedEmission, treasury, mustAmount(t, poolVector.CarryIn))
			require.NoError(t, err)
			require.Equal(t, mustAmount(t, allocVector.Pool).String(), poolE.String(),
				"the two vectors must describe the same epoch pool")

			rows, snapshots := allocationRows(allocVector.BlocksActive)
			_, _, carryOut, _, err := keeper.AllocateSlotEntitlements(1, poolE, rows, snapshots, 1, 1)
			require.NoError(t, err)
			require.Equal(t, mustAmount(t, allocVector.CarryOut).String(), carryOut.String())

			// The next epoch consumes exactly that carry. Asserted as a difference
			// between two production outputs rather than by restating the pool
			// formula here: with the emission and treasury held equal, the two
			// pools may differ only by the change in carry.
			poolNext, err := keeper.ComputeRewardPoolV2(mintedEmission, treasury, carryOut)
			require.NoError(t, err)
			require.Equal(t,
				carryOut.Sub(mustAmount(t, poolVector.CarryIn)).String(),
				poolNext.Sub(poolE).String(),
				"the following epoch's pool must differ from this one only by the change in carry")
		},
	}

	require.Len(t, pack.RequiredAssertions, len(handled),
		"every required assertion must have a check bound to it")
	for _, assertion := range pack.RequiredAssertions {
		check, ok := handled[assertion]
		require.Truef(t, ok, "required assertion %q has no bound check", assertion)
		t.Run(assertion, func(t *testing.T) {
			check(t)
			ledger.Record(packName, section, assertion)
		})
	}
}

func runNegativeDiscriminators(t *testing.T, pack consensusvectors.RewardPack, ledger *consensusvectors.CaseLedger) {
	const section = "negative_discriminators"
	packName := consensusvectors.RewardPackFilename

	for _, v := range pack.NegativeDiscriminators {
		t.Run(v.Name, func(t *testing.T) {
			pool := mustAmount(t, v.Pool)
			rows, snapshots := allocationRows(v.BlocksActive)
			entitlements, allocated, _, _, err := keeper.AllocateSlotEntitlements(
				1, pool, rows, snapshots, 1, 1)
			require.NoError(t, err)

			produced := make([]math.Int, len(v.BlocksActive))
			for i := range produced {
				produced[i] = math.ZeroInt()
			}
			for _, entitlement := range entitlements {
				produced[entitlement.SlotId-1] = mustAmount(t, entitlement.EntitlementAmount)
			}

			switch v.Name {
			case "wrong-denominator-overallocation-must-reject":
				// The correct denominator is the total of active blocks. Using the
				// count of reward-enabled blocks instead over-allocates, and the
				// vector states by how much.
				totalActive := uint64(0)
				for _, blocks := range v.BlocksActive {
					totalActive += blocks.Uint64()
				}
				require.Equal(t, v.CorrectDenominatorW.Uint64(), totalActive, "correct denominator W")

				// Compute the forbidden result explicitly and show it is the one the
				// vector names, so the discriminator is demonstrated rather than
				// assumed.
				wrongDenominator := math.NewIntFromUint64(v.IncorrectDenominatorREB.Uint64())
				wrongAllocated := math.ZeroInt()
				for i, blocks := range v.BlocksActive {
					amount := pool.Mul(math.NewIntFromUint64(blocks.Uint64())).Quo(wrongDenominator)
					require.Equalf(t, mustAmount(t, v.IncorrectEntitlements[i]).String(), amount.String(),
						"forbidden entitlement %d", i)
					wrongAllocated = wrongAllocated.Add(amount)
				}
				require.Equal(t, mustAmount(t, v.IncorrectAllocated).String(), wrongAllocated.String())
				require.Truef(t, wrongAllocated.GT(pool),
					"the forbidden algorithm must over-allocate: %s vs pool %s", wrongAllocated, pool)

				// The production function must not produce it, and must respect the
				// bound the forbidden one breaches.
				require.Truef(t, allocated.LTE(pool),
					"production allocated %s exceeds pool %s", allocated, pool)
				require.NotEqual(t, wrongAllocated.String(), allocated.String(),
					"production reproduced the forbidden over-allocation")

			case "floor-pool-before-multiply-must-not-conform":
				// Flooring pool/W first and multiplying afterwards loses precision
				// that flooring once at the end preserves.
				totalActive := uint64(0)
				for _, blocks := range v.BlocksActive {
					totalActive += blocks.Uint64()
				}
				perBlock := pool.Quo(math.NewIntFromUint64(totalActive))
				for i, blocks := range v.BlocksActive {
					forbidden := perBlock.Mul(math.NewIntFromUint64(blocks.Uint64()))
					require.Equalf(t, mustAmount(t, v.IncorrectFloorPoolThenMultip[i]).String(), forbidden.String(),
						"forbidden entitlement %d", i)
					require.Equalf(t, mustAmount(t, v.CorrectEntitlements[i]).String(), produced[i].String(),
						"canonical entitlement %d", i)
					require.NotEqualf(t, forbidden.String(), produced[i].String(),
						"production reproduced the forbidden order of operations at %d", i)
				}

			default:
				t.Fatalf("negative discriminator %q has no bound check", v.Name)
			}

			require.NotEmpty(t, v.RequiredResult, "discriminator states no required result")
			ledger.Record(packName, section, v.Name)
		})
	}
}

func poolVectorNamed(t *testing.T, pack consensusvectors.RewardPack, name string) consensusvectors.PoolVector {
	t.Helper()
	for _, v := range pack.PoolVectors {
		if v.Name == name {
			return v
		}
	}
	t.Fatalf("pool vector %q is missing", name)
	return consensusvectors.PoolVector{}
}

func allocationVectorNamed(t *testing.T, pack consensusvectors.RewardPack, name string) consensusvectors.AllocationVector {
	t.Helper()
	for _, v := range pack.AllocationVectors {
		if v.Name == name {
			return v
		}
	}
	t.Fatalf("allocation vector %q is missing", name)
	return consensusvectors.AllocationVector{}
}
