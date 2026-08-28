package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/runtime"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/x/coreslot/keeper"
	"github.com/twilight-project/twilight-core/x/coreslot/types"
)

// addr returns a distinct, ordinary, admissible account address.
//
// Deliberately never all-zero: that address is inadmissible by the economic
// rule, so using it as a nominee would make a test pass for the wrong reason.
func addr(marker byte) string {
	raw := make([]byte, 20)
	raw[0] = marker
	raw[19] = marker
	return sdk.AccAddress(raw).String()
}

// authoritySetup returns a keeper whose params carry two DISTINCT, ordinary
// authority addresses.
//
// The shared setup() helper seeds the primary authority with the all-zero
// address, which the economic rule refuses. That is harmless for an incumbent —
// incumbents are never revalidated — but it would make every "nominate back to
// the incumbent" case fail for address admissibility rather than for the reason
// under test.
func authoritySetup(t *testing.T) (types.MsgServer, keeper.Keeper, sdk.Context, string, string) {
	t.Helper()
	k, ctx, _, _ := setup(t)
	primary, emergency := addr(0x11), addr(0x12)
	require.NoError(t, k.Params.Set(ctx, types.DefaultParams(primary, emergency)))
	return keeper.NewMsgServer(k), k, ctx, primary, emergency
}

func holder(t *testing.T, k keeper.Keeper, ctx sdk.Context, role types.AuthorityRole) string {
	t.Helper()
	params, err := k.Params.Get(ctx)
	require.NoError(t, err)
	if role == types.AuthorityRole_AUTHORITY_ROLE_EMERGENCY {
		return params.EmergencyAuthority
	}
	return params.Authority
}

// bothRoles runs a case against each operational role, so neither is covered by
// accident. The emergency role has the same shape and the same consequences, and
// a mechanism that worked for only one of them would be worse than none.
func bothRoles(t *testing.T, name string, run func(t *testing.T, role types.AuthorityRole)) {
	t.Helper()
	for label, role := range map[string]types.AuthorityRole{
		"primary":   types.AuthorityRole_AUTHORITY_ROLE_PRIMARY,
		"emergency": types.AuthorityRole_AUTHORITY_ROLE_EMERGENCY,
	} {
		t.Run(name+"/"+label, func(t *testing.T) { run(t, role) })
	}
}

// A nomination must not change who holds the role. This is the property that
// makes a wrong-but-valid address harmless: until the destination signs, nothing
// about the chain's authorization has moved.
func TestNominationDoesNotChangeTheEffectiveAuthority(t *testing.T) {
	bothRoles(t, "nomination is inert", func(t *testing.T, role types.AuthorityRole) {
		ms, k, ctx, primary, emergency := authoritySetup(t)
		before := holder(t, k, ctx, role)
		nominee := addr(0x21)

		_, err := ms.NominateAuthority(ctx, &types.MsgNominateAuthority{
			Authority: before, Role: role, Nominee: nominee,
		})
		require.NoError(t, err)

		require.Equal(t, before, holder(t, k, ctx, role), "the incumbent must still hold the role")

		// And the OTHER role is untouched.
		params, err := k.Params.Get(ctx)
		require.NoError(t, err)
		require.Equal(t, primary, params.Authority)
		require.Equal(t, emergency, params.EmergencyAuthority)
	})
}

func TestOnlyTheCurrentRoleHolderMayNominate(t *testing.T) {
	bothRoles(t, "stranger refused", func(t *testing.T, role types.AuthorityRole) {
		ms, k, ctx, _, _ := authoritySetup(t)
		_, err := ms.NominateAuthority(ctx, &types.MsgNominateAuthority{
			Authority: addr(0x99), Role: role, Nominee: addr(0x21),
		})
		require.ErrorIs(t, err, types.ErrUnauthorized)
		_, getErr := k.PendingAuthority.Get(ctx, int32(role))
		require.Error(t, getErr, "a refused nomination must leave no pending state")
	})

	// The holder of one role may not nominate for the other. Either direction
	// would let one role quietly absorb the other.
	t.Run("the other role's holder is refused", func(t *testing.T) {
		ms, _, ctx, primary, emergency := authoritySetup(t)
		_, err := ms.NominateAuthority(ctx, &types.MsgNominateAuthority{
			Authority: emergency, Role: types.AuthorityRole_AUTHORITY_ROLE_PRIMARY, Nominee: addr(0x21),
		})
		require.ErrorIs(t, err, types.ErrUnauthorized)

		_, err = ms.NominateAuthority(ctx, &types.MsgNominateAuthority{
			Authority: primary, Role: types.AuthorityRole_AUTHORITY_ROLE_EMERGENCY, Nominee: addr(0x21),
		})
		require.ErrorIs(t, err, types.ErrUnauthorized)
	})
}

// The destinations the economic rule refuses are exactly the ones from which
// there is no return: nobody can sign for them, so the capability the role gates
// is gone permanently.
func TestInadmissibleDestinationsAreRefusedAtNomination(t *testing.T) {
	blockedAddr := addr(0x55)

	for name, nominee := range map[string]string{
		"all-zero address": sdk.AccAddress(make([]byte, 20)).String(),
		"malformed":        "not-an-address",
	} {
		bothRoles(t, "refuses "+name, func(t *testing.T, role types.AuthorityRole) {
			ms, k, ctx, _, _ := authoritySetup(t)
			_, err := ms.NominateAuthority(ctx, &types.MsgNominateAuthority{
				Authority: holder(t, k, ctx, role), Role: role, Nominee: nominee,
			})
			require.ErrorIs(t, err, types.ErrInvalidAddress)
		})
	}

	t.Run("refuses a bank-blocked address", func(t *testing.T) {
		k, ctx, _, _ := setup(t, blockedAddr)
		primary, emergency := addr(0x11), addr(0x12)
		require.NoError(t, k.Params.Set(ctx, types.DefaultParams(primary, emergency)))
		ms := keeper.NewMsgServer(k)

		_, err := ms.NominateAuthority(ctx, &types.MsgNominateAuthority{
			Authority: primary, Role: types.AuthorityRole_AUTHORITY_ROLE_PRIMARY, Nominee: blockedAddr,
		})
		require.ErrorIs(t, err, types.ErrInvalidAddress)
	})
}

func TestNominatingTheIncumbentIsRefusedAsANoOp(t *testing.T) {
	bothRoles(t, "no-op refused", func(t *testing.T, role types.AuthorityRole) {
		ms, k, ctx, _, _ := authoritySetup(t)
		incumbent := holder(t, k, ctx, role)
		_, err := ms.NominateAuthority(ctx, &types.MsgNominateAuthority{
			Authority: incumbent, Role: role, Nominee: incumbent,
		})
		require.ErrorIs(t, err, types.ErrInvalidAddress)
	})
}

// An unspecified role must never be treated as a default. A message that omitted
// the field would otherwise rotate the primary authority — the more
// consequential role, and the one least likely to have been meant by omission.
func TestUnspecifiedAndUnknownRolesAreRefused(t *testing.T) {
	ms, _, ctx, primary, _ := authoritySetup(t)

	for name, role := range map[string]types.AuthorityRole{
		"unspecified":  types.AuthorityRole_AUTHORITY_ROLE_UNSPECIFIED,
		"out of range": types.AuthorityRole(99),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ms.NominateAuthority(ctx, &types.MsgNominateAuthority{
				Authority: primary, Role: role, Nominee: addr(0x21),
			})
			require.ErrorIs(t, err, types.ErrInvalidAuthorityRole)

			_, err = ms.AcceptAuthority(ctx, &types.MsgAcceptAuthority{Nominee: addr(0x21), Role: role})
			require.ErrorIs(t, err, types.ErrInvalidAuthorityRole)

			_, err = ms.CancelAuthorityNomination(ctx, &types.MsgCancelAuthorityNomination{
				Authority: primary, Role: role,
			})
			require.ErrorIs(t, err, types.ErrInvalidAuthorityRole)
		})
	}
}

// Acceptance is the whole mechanism: the signature proves the destination key
// exists and is controlled.
func TestOnlyTheExactNomineeMayAccept(t *testing.T) {
	bothRoles(t, "wrong signer refused", func(t *testing.T, role types.AuthorityRole) {
		ms, k, ctx, _, _ := authoritySetup(t)
		incumbent := holder(t, k, ctx, role)
		nominee := addr(0x21)

		_, err := ms.NominateAuthority(ctx, &types.MsgNominateAuthority{
			Authority: incumbent, Role: role, Nominee: nominee,
		})
		require.NoError(t, err)

		// Neither a stranger nor the incumbent may complete the handover.
		for _, signer := range []string{addr(0x99), incumbent} {
			_, err = ms.AcceptAuthority(ctx, &types.MsgAcceptAuthority{Nominee: signer, Role: role})
			require.ErrorIs(t, err, types.ErrUnauthorized)
			require.Equal(t, incumbent, holder(t, k, ctx, role))
		}
	})
}

func TestAcceptanceRotatesOnlyTheSelectedRole(t *testing.T) {
	bothRoles(t, "handover completes", func(t *testing.T, role types.AuthorityRole) {
		ms, k, ctx, primary, emergency := authoritySetup(t)
		incumbent := holder(t, k, ctx, role)
		nominee := addr(0x21)

		before, err := k.Params.Get(ctx)
		require.NoError(t, err)

		_, err = ms.NominateAuthority(ctx, &types.MsgNominateAuthority{
			Authority: incumbent, Role: role, Nominee: nominee,
		})
		require.NoError(t, err)
		_, err = ms.AcceptAuthority(ctx, &types.MsgAcceptAuthority{Nominee: nominee, Role: role})
		require.NoError(t, err)

		require.Equal(t, nominee, holder(t, k, ctx, role), "the successor must hold the role")

		after, err := k.Params.Get(ctx)
		require.NoError(t, err)
		if role == types.AuthorityRole_AUTHORITY_ROLE_PRIMARY {
			require.Equal(t, emergency, after.EmergencyAuthority, "the other role must be untouched")
		} else {
			require.Equal(t, primary, after.Authority, "the other role must be untouched")
		}

		// Every unrelated parameter survives: acceptance rotates a role, it is not
		// an incidental parameter write.
		expected := before
		if role == types.AuthorityRole_AUTHORITY_ROLE_PRIMARY {
			expected.Authority = nominee
		} else {
			expected.EmergencyAuthority = nominee
		}
		require.Equal(t, expected, after)

		// The pending record is cleared in the same handler, so a displaced nominee
		// can never act on a completed handover.
		_, getErr := k.PendingAuthority.Get(ctx, int32(role))
		require.Error(t, getErr)

		// And the incumbent has genuinely lost the role.
		_, err = ms.NominateAuthority(ctx, &types.MsgNominateAuthority{
			Authority: incumbent, Role: role, Nominee: addr(0x31),
		})
		require.ErrorIs(t, err, types.ErrUnauthorized)
	})
}

func TestReplacementInvalidatesTheDisplacedNominee(t *testing.T) {
	bothRoles(t, "replaced nominee cannot accept", func(t *testing.T, role types.AuthorityRole) {
		ms, k, ctx, _, _ := authoritySetup(t)
		incumbent := holder(t, k, ctx, role)
		first, second := addr(0x21), addr(0x22)

		_, err := ms.NominateAuthority(ctx, &types.MsgNominateAuthority{
			Authority: incumbent, Role: role, Nominee: first,
		})
		require.NoError(t, err)
		_, err = ms.NominateAuthority(ctx, &types.MsgNominateAuthority{
			Authority: incumbent, Role: role, Nominee: second,
		})
		require.NoError(t, err)

		_, err = ms.AcceptAuthority(ctx, &types.MsgAcceptAuthority{Nominee: first, Role: role})
		require.ErrorIs(t, err, types.ErrUnauthorized)
		require.Equal(t, incumbent, holder(t, k, ctx, role))

		_, err = ms.AcceptAuthority(ctx, &types.MsgAcceptAuthority{Nominee: second, Role: role})
		require.NoError(t, err)
		require.Equal(t, second, holder(t, k, ctx, role))
	})
}

// Cancellation is what makes a mistaken nomination correctable rather than
// merely inert.
func TestCancellationClearsThePendingNomination(t *testing.T) {
	bothRoles(t, "cancel then accept fails", func(t *testing.T, role types.AuthorityRole) {
		ms, k, ctx, _, _ := authoritySetup(t)
		incumbent := holder(t, k, ctx, role)
		nominee := addr(0x21)

		_, err := ms.NominateAuthority(ctx, &types.MsgNominateAuthority{
			Authority: incumbent, Role: role, Nominee: nominee,
		})
		require.NoError(t, err)

		// A stranger cannot cancel.
		_, err = ms.CancelAuthorityNomination(ctx, &types.MsgCancelAuthorityNomination{
			Authority: addr(0x99), Role: role,
		})
		require.ErrorIs(t, err, types.ErrUnauthorized)

		_, err = ms.CancelAuthorityNomination(ctx, &types.MsgCancelAuthorityNomination{
			Authority: incumbent, Role: role,
		})
		require.NoError(t, err)

		_, getErr := k.PendingAuthority.Get(ctx, int32(role))
		require.Error(t, getErr)

		_, err = ms.AcceptAuthority(ctx, &types.MsgAcceptAuthority{Nominee: nominee, Role: role})
		require.ErrorIs(t, err, types.ErrNoPendingNomination)
		require.Equal(t, incumbent, holder(t, k, ctx, role))
	})

	t.Run("canceling nothing is refused", func(t *testing.T) {
		ms, _, ctx, primary, _ := authoritySetup(t)
		_, err := ms.CancelAuthorityNomination(ctx, &types.MsgCancelAuthorityNomination{
			Authority: primary, Role: types.AuthorityRole_AUTHORITY_ROLE_PRIMARY,
		})
		require.ErrorIs(t, err, types.ErrNoPendingNomination)
	})
}

// The two roles must not share pending state. A nomination for one must be
// invisible to the other.
func TestBothRolesRotateIndependently(t *testing.T) {
	ms, k, ctx, primary, emergency := authoritySetup(t)
	newPrimary, newEmergency := addr(0x21), addr(0x22)

	_, err := ms.NominateAuthority(ctx, &types.MsgNominateAuthority{
		Authority: primary, Role: types.AuthorityRole_AUTHORITY_ROLE_PRIMARY, Nominee: newPrimary,
	})
	require.NoError(t, err)
	_, err = ms.NominateAuthority(ctx, &types.MsgNominateAuthority{
		Authority: emergency, Role: types.AuthorityRole_AUTHORITY_ROLE_EMERGENCY, Nominee: newEmergency,
	})
	require.NoError(t, err)

	// Accepting one leaves the other pending and unchanged.
	_, err = ms.AcceptAuthority(ctx, &types.MsgAcceptAuthority{
		Nominee: newPrimary, Role: types.AuthorityRole_AUTHORITY_ROLE_PRIMARY,
	})
	require.NoError(t, err)

	params, err := k.Params.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, newPrimary, params.Authority)
	require.Equal(t, emergency, params.EmergencyAuthority, "the emergency handover is still pending")

	stillPending, err := k.PendingAuthority.Get(ctx, int32(types.AuthorityRole_AUTHORITY_ROLE_EMERGENCY))
	require.NoError(t, err)
	require.Equal(t, newEmergency, stillPending.Nominee)

	_, err = ms.AcceptAuthority(ctx, &types.MsgAcceptAuthority{
		Nominee: newEmergency, Role: types.AuthorityRole_AUTHORITY_ROLE_EMERGENCY,
	})
	require.NoError(t, err)
	params, err = k.Params.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, newEmergency, params.EmergencyAuthority)
}

// UpdateParams is no longer a rotation path. Before this, editing an unrelated
// field meant re-supplying both authority addresses correctly, and getting one
// wrong ended governance.
func TestUpdateParamsCannotChangeEitherAuthority(t *testing.T) {
	for name, mutate := range map[string]func(p *types.Params){
		"primary":   func(p *types.Params) { p.Authority = addr(0x21) },
		"emergency": func(p *types.Params) { p.EmergencyAuthority = addr(0x22) },
	} {
		t.Run(name, func(t *testing.T) {
			ms, k, ctx, primary, emergency := authoritySetup(t)
			params, err := k.Params.Get(ctx)
			require.NoError(t, err)
			mutate(&params)

			_, err = ms.UpdateParams(ctx, &types.MsgUpdateParams{Authority: primary, Params: &params})
			// Refused, not silently corrected: a caller sending a stale document must
			// not believe the document it sent was applied.
			require.ErrorIs(t, err, types.ErrInvalidParams)

			after, err := k.Params.Get(ctx)
			require.NoError(t, err)
			require.Equal(t, primary, after.Authority)
			require.Equal(t, emergency, after.EmergencyAuthority)
		})
	}

	t.Run("an unrelated parameter change still works", func(t *testing.T) {
		ms, k, ctx, primary, emergency := authoritySetup(t)
		params, err := k.Params.Get(ctx)
		require.NoError(t, err)
		params.MaxActiveSlots = params.MaxActiveSlots - 1

		_, err = ms.UpdateParams(ctx, &types.MsgUpdateParams{Authority: primary, Params: &params})
		require.NoError(t, err)

		after, err := k.Params.Get(ctx)
		require.NoError(t, err)
		require.Equal(t, params.MaxActiveSlots, after.MaxActiveSlots)
		require.Equal(t, primary, after.Authority)
		require.Equal(t, emergency, after.EmergencyAuthority)
	})
}

// genesisAuthoritySetup builds a keeper through InitGenesis with one ACTIVE
// slot, which the fresh-genesis rules require, so the resulting state can be
// exported and re-imported for real rather than only inspected.
func genesisAuthoritySetup(t *testing.T) (types.MsgServer, keeper.Keeper, sdk.Context, string, string) {
	t.Helper()
	k, ctx, _, _ := setup(t)
	primary, emergency := addr(0x11), addr(0x12)
	_, err := initGenesis(t, k, ctx, &types.GenesisState{
		Params: ptrParams(types.DefaultParams(primary, emergency)),
		Slots:  []*types.CoreSlot{slot(t, 1, addr(0x41), 0x41, types.SlotStatus_SLOT_STATUS_ACTIVE, 1)},
	})
	require.NoError(t, err)
	return keeper.NewMsgServer(k), k, ctx, primary, emergency
}

func ptrParams(p types.Params) *types.Params { return &p }

// A pending nomination must survive export and import. Dropping it would strand
// a rotation mid-flight: the incumbent believes a handover is pending, and the
// nominee finds nothing to accept.
func TestGenesisRoundTripPreservesPendingNominations(t *testing.T) {
	ms, k, ctx, primary, emergency := genesisAuthoritySetup(t)
	newPrimary, newEmergency := addr(0x21), addr(0x22)

	_, err := ms.NominateAuthority(ctx, &types.MsgNominateAuthority{
		Authority: primary, Role: types.AuthorityRole_AUTHORITY_ROLE_PRIMARY, Nominee: newPrimary,
	})
	require.NoError(t, err)
	_, err = ms.NominateAuthority(ctx, &types.MsgNominateAuthority{
		Authority: emergency, Role: types.AuthorityRole_AUTHORITY_ROLE_EMERGENCY, Nominee: newEmergency,
	})
	require.NoError(t, err)

	exported, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Len(t, exported.PendingAuthorityTransfers, 2)

	// Int32Key iterates ascending, so the exported order is role order and is
	// deterministic — not an artifact of map iteration.
	require.Equal(t, types.AuthorityRole_AUTHORITY_ROLE_PRIMARY, exported.PendingAuthorityTransfers[0].Role)
	require.Equal(t, types.AuthorityRole_AUTHORITY_ROLE_EMERGENCY, exported.PendingAuthorityTransfers[1].Role)
	require.Equal(t, newPrimary, exported.PendingAuthorityTransfers[0].Transfer.Nominee)
	require.Equal(t, newEmergency, exported.PendingAuthorityTransfers[1].Transfer.Nominee)

	// Re-import into a fresh keeper and complete the handover from imported state,
	// which proves the nomination is usable and not merely present.
	k2, ctx2, _, _ := setup(t)
	_, err = k2.InitGenesis(ctx2, exported)
	require.NoError(t, err)

	restored, err := k2.PendingAuthority.Get(ctx2, int32(types.AuthorityRole_AUTHORITY_ROLE_PRIMARY))
	require.NoError(t, err)
	require.Equal(t, newPrimary, restored.Nominee)

	_, err = keeper.NewMsgServer(k2).AcceptAuthority(ctx2, &types.MsgAcceptAuthority{
		Nominee: newPrimary, Role: types.AuthorityRole_AUTHORITY_ROLE_PRIMARY,
	})
	require.NoError(t, err)
	require.Equal(t, newPrimary, holder(t, k2, ctx2, types.AuthorityRole_AUTHORITY_ROLE_PRIMARY))
}

// A fresh genesis has no nominations. Anything else would mean a chain launched
// with a handover already in flight that nobody authorized.
func TestFreshGenesisCarriesNoPendingNominations(t *testing.T) {
	_, k, ctx, _, _ := genesisAuthoritySetup(t)
	exported, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Empty(t, exported.PendingAuthorityTransfers)
}

// Imported nominations get the same admissibility rule a live nomination gets.
// A document is not a trusted source: without this, an inadmissible destination
// could be installed by writing it into genesis by hand.
func TestImportedNominationsAreValidated(t *testing.T) {
	entry := func(role types.AuthorityRole, nominee string, height int64) *types.PendingAuthorityTransferEntry {
		return &types.PendingAuthorityTransferEntry{
			Role:     role,
			Transfer: &types.PendingAuthorityTransfer{Nominee: nominee, NominatedHeight: height},
		}
	}

	for name, transfers := range map[string][]*types.PendingAuthorityTransferEntry{
		"unspecified role": {entry(types.AuthorityRole_AUTHORITY_ROLE_UNSPECIFIED, addr(0x21), 1)},
		"unknown role":     {entry(types.AuthorityRole(99), addr(0x21), 1)},
		"duplicate role": {
			entry(types.AuthorityRole_AUTHORITY_ROLE_PRIMARY, addr(0x21), 1),
			entry(types.AuthorityRole_AUTHORITY_ROLE_PRIMARY, addr(0x22), 1),
		},
		"all-zero nominee": {
			entry(types.AuthorityRole_AUTHORITY_ROLE_PRIMARY, sdk.AccAddress(make([]byte, 20)).String(), 1),
		},
		"malformed nominee": {entry(types.AuthorityRole_AUTHORITY_ROLE_PRIMARY, "not-an-address", 1)},
		"negative height":   {entry(types.AuthorityRole_AUTHORITY_ROLE_PRIMARY, addr(0x21), -1)},
		"empty transfer":    {{Role: types.AuthorityRole_AUTHORITY_ROLE_PRIMARY}},
		"nil entry":         {nil},
	} {
		t.Run(name, func(t *testing.T) {
			// A genesis known to be valid apart from the transfers under test, so a
			// rejection can only be about them.
			_, k, ctx, _, _ := genesisAuthoritySetup(t)
			genesis, err := k.ExportGenesis(ctx)
			require.NoError(t, err)
			genesis.PendingAuthorityTransfers = transfers

			fresh, freshCtx, _, _ := setup(t)
			_, err = fresh.InitGenesis(freshCtx, genesis)
			require.Error(t, err, "an unusable nomination must stop genesis rather than be imported")
		})
	}
}

// A module account is the destination the whole guard exists for: an ordinary,
// well-formed bech32 address that no key controls. Installing one as an authority
// removes every capability that role gates, permanently — for the primary role
// that includes the upgrade path, so the chain could not even upgrade out of it.
//
// Asserted explicitly rather than left to the all-zero case. The two are refused
// by different branches of the economic rule (ErrModuleAccount vs the zero
// check), and only one of them is a plausible operator mistake.
func TestModuleAccountIsRefusedAsANominee(t *testing.T) {
	bothRoles(t, "module account refused", func(t *testing.T, role types.AuthorityRole) {
		ms, k, ctx, _, _ := authoritySetup(t)
		incumbent := holder(t, k, ctx, role)

		_, err := ms.NominateAuthority(ctx, &types.MsgNominateAuthority{
			Authority: incumbent, Role: role, Nominee: testModuleAddress(testModuleAccountName),
		})
		require.ErrorIs(t, err, types.ErrInvalidAddress)
		require.Contains(t, err.Error(), "module account")

		_, getErr := k.PendingAuthority.Get(ctx, int32(role))
		require.Error(t, getErr, "a refused nomination must leave no pending state")
		require.Equal(t, incumbent, holder(t, k, ctx, role))
	})
}

// Acceptance re-checks admissibility, and this proves the check is INDEPENDENT of
// the one at nomination rather than a second call with the same answer.
//
// The nominee is admissible when nominated and inadmissible when the acceptance
// executes. That is not contrived: a nomination is durable state that can outlive
// a binary upgrade, and the inadmissible set is app-derived — a bank-blocked
// address added between the two steps produces exactly this.
//
// The seam is the injected validator. A second keeper is built over the SAME
// store with a stricter blocked set, so the pending nomination written by the
// first is read by the second under the newer rule.
func TestAcceptanceRevalidatesUnderCurrentRules(t *testing.T) {
	bothRoles(t, "newly blocked nominee cannot accept", func(t *testing.T, role types.AuthorityRole) {
		lenient, ctx, _, _, storeKey := setupWithRawStore(t)
		primary, emergency := addr(0x11), addr(0x12)
		require.NoError(t, lenient.Params.Set(ctx, types.DefaultParams(primary, emergency)))

		incumbent := primary
		if role == types.AuthorityRole_AUTHORITY_ROLE_EMERGENCY {
			incumbent = emergency
		}
		nominee := addr(0x21)

		// Admissible now.
		_, err := keeper.NewMsgServer(lenient).NominateAuthority(ctx, &types.MsgNominateAuthority{
			Authority: incumbent, Role: role, Nominee: nominee,
		})
		require.NoError(t, err)

		// The same store, read by a keeper whose rules now refuse that address.
		strict := keeper.NewKeeper(
			authorityTestCodec(), runtime.NewKVStoreService(storeKey), testEconomicAddresses(t, nominee), nil)

		pending, err := strict.PendingAuthority.Get(ctx, int32(role))
		require.NoError(t, err, "the nomination must still be there to be re-judged")
		require.Equal(t, nominee, pending.Nominee)

		_, err = keeper.NewMsgServer(strict).AcceptAuthority(ctx, &types.MsgAcceptAuthority{
			Nominee: nominee, Role: role,
		})
		require.ErrorIs(t, err, types.ErrInvalidAddress,
			"a destination inadmissible under CURRENT rules must not be installed because it was admissible when named")

		// The incumbent keeps the role, and the nomination is left pending rather
		// than silently cleared — a refused acceptance is not a cancellation.
		require.Equal(t, incumbent, holder(t, strict, ctx, role))
		_, err = strict.PendingAuthority.Get(ctx, int32(role))
		require.NoError(t, err)
	})
}

// authorityTestCodec builds the same codec the shared setup uses, so a keeper
// rebuilt over an existing store decodes what the first one wrote.
func authorityTestCodec() codec.Codec {
	registry := codectypes.NewInterfaceRegistry()
	types.RegisterInterfaces(registry)
	return codec.NewProtoCodec(registry)
}

// Malformed pending state must be refused by ORDINARY genesis validation, not
// only by keeper import.
//
// The distinction is operational. `coreslot-genesis validate` and the module's
// ValidateGenesis run the pure types-level check; before it covered pending
// transfers, a document naming role 99 with a malformed nominee and a negative
// height was reported as "a valid genesis file" and refused only later, during
// import, after params had already been written to the InitChain cache. Tooling
// that says a broken document is fine is worse than tooling that says nothing.
func TestGenesisValidateRejectsMalformedPendingTransfers(t *testing.T) {
	entry := func(role types.AuthorityRole, nominee string, height int64) *types.PendingAuthorityTransferEntry {
		return &types.PendingAuthorityTransferEntry{
			Role:     role,
			Transfer: &types.PendingAuthorityTransfer{Nominee: nominee, NominatedHeight: height},
		}
	}

	for name, tc := range map[string]struct {
		transfers []*types.PendingAuthorityTransferEntry
		wantErr   string
	}{
		"nil entry":    {[]*types.PendingAuthorityTransferEntry{nil}, "is nil"},
		"nil transfer": {[]*types.PendingAuthorityTransferEntry{{Role: types.AuthorityRole_AUTHORITY_ROLE_PRIMARY}}, "no transfer"},
		"unspecified role": {
			[]*types.PendingAuthorityTransferEntry{entry(types.AuthorityRole_AUTHORITY_ROLE_UNSPECIFIED, addr(0x21), 1)},
			"invalid role",
		},
		"unknown role": {
			[]*types.PendingAuthorityTransferEntry{entry(types.AuthorityRole(99), addr(0x21), 1)},
			"invalid role",
		},
		"duplicate role": {
			[]*types.PendingAuthorityTransferEntry{
				entry(types.AuthorityRole_AUTHORITY_ROLE_PRIMARY, addr(0x21), 1),
				entry(types.AuthorityRole_AUTHORITY_ROLE_PRIMARY, addr(0x22), 1),
			},
			"duplicate",
		},
		"malformed nominee": {
			[]*types.PendingAuthorityTransferEntry{entry(types.AuthorityRole_AUTHORITY_ROLE_PRIMARY, "not-an-address", 1)},
			"invalid nominee",
		},
		"negative height": {
			[]*types.PendingAuthorityTransferEntry{entry(types.AuthorityRole_AUTHORITY_ROLE_PRIMARY, addr(0x21), -1)},
			"negative nominated height",
		},
		// Runtime nomination refuses naming the incumbent, so a document a real
		// chain produced cannot contain one. Refusing it keeps imported state
		// canonical with state the chain can actually reach.
		"nominee is the incumbent": {
			[]*types.PendingAuthorityTransferEntry{entry(types.AuthorityRole_AUTHORITY_ROLE_PRIMARY, addr(0x11), 1)},
			"names the current holder",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, k, ctx, _, _ := genesisAuthoritySetup(t)
			genesis, err := k.ExportGenesis(ctx)
			require.NoError(t, err)
			genesis.PendingAuthorityTransfers = tc.transfers

			err = genesis.Validate()
			require.Error(t, err, "pure genesis validation must reject this without a keeper")
			require.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// A module account passes every pure check — it is well-formed bech32 — so only
// the keeper's app-derived preflight can refuse it. It must do so BEFORE the
// first store write, or a document that fails halfway leaves params behind.
func TestGenesisPreflightRejectsInadmissiblePendingNominee(t *testing.T) {
	_, k, ctx, _, _ := genesisAuthoritySetup(t)
	genesis, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	genesis.PendingAuthorityTransfers = []*types.PendingAuthorityTransferEntry{{
		Role: types.AuthorityRole_AUTHORITY_ROLE_PRIMARY,
		Transfer: &types.PendingAuthorityTransfer{
			Nominee: testModuleAddress(testModuleAccountName), NominatedHeight: 1,
		},
	}}

	// Pure validation cannot see this: module-account membership is app-derived.
	require.NoError(t, genesis.Validate(),
		"a module account is well-formed bech32, so the types layer must not be expected to catch it")

	fresh, freshCtx, _, _ := setup(t)
	_, err = fresh.InitGenesis(freshCtx, genesis)
	require.Error(t, err)
	require.Contains(t, err.Error(), "module account")

	// Nothing was committed: params must not exist on a keeper whose genesis was
	// refused, which is what proves the check ran before the first write.
	_, paramsErr := fresh.Params.Get(freshCtx)
	require.Error(t, paramsErr, "a refused genesis must leave no params behind")
}
