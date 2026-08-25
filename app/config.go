package app

import (
	runtimev1alpha1 "cosmossdk.io/api/cosmos/app/runtime/v1alpha1"
	appv1alpha1 "cosmossdk.io/api/cosmos/app/v1alpha1"
	authmodulev1 "cosmossdk.io/api/cosmos/auth/module/v1"
	bankmodulev1 "cosmossdk.io/api/cosmos/bank/module/v1"
	consensusmodulev1 "cosmossdk.io/api/cosmos/consensus/module/v1"
	txconfigv1 "cosmossdk.io/api/cosmos/tx/config/v1"
	upgrademodulev1 "cosmossdk.io/api/cosmos/upgrade/module/v1"
	"cosmossdk.io/core/appconfig"
	"cosmossdk.io/depinject"

	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	rewardstypes "github.com/twilight-project/twilight-core/x/rewards/types"
)

// moduleAccountPermissions is the single authoritative declaration of this
// chain's module accounts.
//
// It feeds two consumers that must never disagree: the auth module's own
// configuration below, and the economic-address validator, which has to know
// which addresses are module accounts in order to refuse them as payees. A
// second hand-maintained list would be a denylist that drifts the first time
// someone adds a module account and updates only one of the two — which is
// exactly what §25 forbids.
var moduleAccountPermissions = []*authmodulev1.ModuleAccountPermission{
	{Account: authtypes.FeeCollectorName},
	{Account: AuthorityModuleName},
	{Account: EmergencyAuthorityModuleName},
	// rewards is the only minter; the fee pool holds no
	// permissions and stays dormant in v1.
	{Account: rewardstypes.ModuleName, Permissions: []string{authtypes.Minter}},
	{Account: rewardstypes.FeePoolName},
}

// ModuleAccountNames returns the module-account names declared above, in
// declaration order. It is derived from the same value the auth module is
// configured with, so a module account cannot exist without the economic-address
// validator knowing about it.
func ModuleAccountNames() []string {
	names := make([]string, 0, len(moduleAccountPermissions))
	for _, permission := range moduleAccountPermissions {
		names = append(names, permission.Account)
	}
	return names
}

var AppConfig = depinject.Configs(
	appconfig.Compose(&appv1alpha1.Config{
		Modules: []*appv1alpha1.ModuleConfig{
			{
				Name: "runtime",
				Config: appconfig.WrapAny(&runtimev1alpha1.Module{
					AppName: Name,
					// auth and bank initialize module accounts before rewards
					// genesis runs; coreslot precedes rewards by convention.
					// mining initializes last: its genesis cross-checks already-imported
					// CoreSlot policies against the initial Selection parameters, and
					// x/coreslot must gain no dependency on x/mining for that check.
					InitGenesis: []string{"upgrade", "auth", "bank", "consensus", "coreslot", "rewards", "mining"},
					// x/upgrade executes a scheduled plan in PreBlock, before any
					// BeginBlocker runs. Omitting it here is silent and total: plans
					// are still stored and still queryable, but nothing ever applies
					// them, so the chain sails past its own halt height and the
					// upgrade appears to have been ignored.
					//
					// Every module that HAS a PreBlocker must be listed: the module
					// manager refuses a partial list rather than silently skipping
					// what was left out. x/auth has one (the unordered-transaction
					// manager), so it appears here even though nothing about the
					// upgrade path needs it. Upgrade runs first, matching the SDK's
					// own ordering — a scheduled halt must be decided before any
					// other module does per-block work.
					PreBlockers: []string{"upgrade", "auth"},
					// rewards credits active blocks at BeginBlock; coreslot has no
					// BeginBlocker.
					BeginBlockers: []string{"rewards"},
					// coreslot remains the sole validator-update emitter and runs
					// first; rewards finalizes epoch accounting after; mining then
					// advances the settlement clock and materializes settlements for
					// whatever epoch rewards just closed, so it must run last.
					EndBlockers:       []string{"coreslot", "rewards", "mining"},
					OverrideStoreKeys: []*runtimev1alpha1.StoreKeyConfig{{ModuleName: "auth", KvStoreKey: "acc"}},
				}),
			},
			{
				Name: "auth",
				Config: appconfig.WrapAny(&authmodulev1.Module{
					Bech32Prefix:             AccountPrefix,
					ModuleAccountPermissions: moduleAccountPermissions,
					Authority:                AuthorityModuleName,
				}),
			},
			{Name: "bank", Config: appconfig.WrapAny(&bankmodulev1.Module{Authority: AuthorityModuleName})},
			// x/upgrade defaults its authority to the governance module account,
			// which is why the module is often assumed to require governance. It
			// does not. Pointing it at the authority this chain already uses for
			// auth, bank and consensus adds no new trust assumption: the role that
			// admits validators and updates parameters also schedules upgrades.
			// See ADR-0003.
			{Name: "upgrade", Config: appconfig.WrapAny(&upgrademodulev1.Module{Authority: AuthorityModuleName})},
			{Name: "consensus", Config: appconfig.WrapAny(&consensusmodulev1.Module{Authority: AuthorityModuleName})},
			{Name: "tx", Config: appconfig.WrapAny(&txconfigv1.Config{})},
		},
	}),
)
