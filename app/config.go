package app

import (
	runtimev1alpha1 "cosmossdk.io/api/cosmos/app/runtime/v1alpha1"
	appv1alpha1 "cosmossdk.io/api/cosmos/app/v1alpha1"
	authmodulev1 "cosmossdk.io/api/cosmos/auth/module/v1"
	bankmodulev1 "cosmossdk.io/api/cosmos/bank/module/v1"
	consensusmodulev1 "cosmossdk.io/api/cosmos/consensus/module/v1"
	txconfigv1 "cosmossdk.io/api/cosmos/tx/config/v1"
	"cosmossdk.io/core/appconfig"
	"cosmossdk.io/depinject"

	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	rewardstypes "github.com/twilight-project/twilight-core/x/rewards/types"
)

var AppConfig = depinject.Configs(
	appconfig.Compose(&appv1alpha1.Config{
		Modules: []*appv1alpha1.ModuleConfig{
			{
				Name: "runtime",
				Config: appconfig.WrapAny(&runtimev1alpha1.Module{
					AppName: Name,
					// auth and bank initialize module accounts before rewards
					// genesis runs; coreslot precedes rewards by convention.
					InitGenesis: []string{"auth", "bank", "consensus", "coreslot", "rewards"},
					// rewards credits active blocks at BeginBlock; coreslot has no
					// BeginBlocker.
					BeginBlockers: []string{"rewards"},
					// coreslot remains the sole validator-update emitter and runs
					// first; rewards finalizes epoch accounting after.
					EndBlockers:       []string{"coreslot", "rewards"},
					OverrideStoreKeys: []*runtimev1alpha1.StoreKeyConfig{{ModuleName: "auth", KvStoreKey: "acc"}},
				}),
			},
			{
				Name: "auth",
				Config: appconfig.WrapAny(&authmodulev1.Module{
					Bech32Prefix: AccountPrefix,
					ModuleAccountPermissions: []*authmodulev1.ModuleAccountPermission{
						{Account: authtypes.FeeCollectorName},
						{Account: AuthorityModuleName},
						{Account: EmergencyAuthorityModuleName},
						// rewards is the only minter; the fee pool holds no
						// permissions and stays dormant in v1.
						{Account: rewardstypes.ModuleName, Permissions: []string{authtypes.Minter}},
						{Account: rewardstypes.FeePoolName},
					},
					Authority: AuthorityModuleName,
				}),
			},
			{Name: "bank", Config: appconfig.WrapAny(&bankmodulev1.Module{Authority: AuthorityModuleName})},
			{Name: "consensus", Config: appconfig.WrapAny(&consensusmodulev1.Module{Authority: AuthorityModuleName})},
			{Name: "tx", Config: appconfig.WrapAny(&txconfigv1.Config{})},
		},
	}),
)
