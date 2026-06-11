package app_test

import (
	"testing"

	dbm "github.com/cosmos/cosmos-db"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/log"

	"github.com/cosmos/cosmos-sdk/testutil/sims"
	"github.com/cosmos/cosmos-sdk/types/module"

	"github.com/twilight-project/twilight-core/app"
)

func TestStakingRoutesAndModuleAreOmitted(t *testing.T) {
	a := app.New(log.NewNopLogger(), dbm.NewMemDB(), nil, true, sims.EmptyAppOptions{})
	require.Nil(t, a.MsgServiceRouter().HandlerByTypeURL("/cosmos.staking.v1beta1.MsgDelegate"))
	require.Nil(t, a.MsgServiceRouter().HandlerByTypeURL("/cosmos.staking.v1beta1.MsgCreateValidator"))
	for _, omitted := range []string{"staking", "distribution", "slashing", "gov", "mint"} {
		_, exists := a.ModuleManager.Modules[omitted]
		require.Falsef(t, exists, "%s module must remain omitted", omitted)
	}
	require.Contains(t, a.ModuleManager.Modules, "coreslot")
	require.Contains(t, a.ModuleManager.Modules, "rewards")

	emitters := 0
	for _, appModule := range a.ModuleManager.Modules {
		if _, ok := appModule.(module.HasABCIEndBlock); ok {
			emitters++
		}
	}
	require.Equal(t, 1, emitters, "coreslot must be the only validator-update-capable EndBlock module")
}
