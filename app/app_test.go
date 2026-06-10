package app_test

import (
	"testing"

	dbm "github.com/cosmos/cosmos-db"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/log"

	"github.com/cosmos/cosmos-sdk/testutil/sims"
	"github.com/cosmos/cosmos-sdk/types/module"

	"github.com/nyks/nyks-core/app"
)

func TestStakingRoutesAndModuleAreOmitted(t *testing.T) {
	a := app.New(log.NewNopLogger(), dbm.NewMemDB(), nil, true, sims.EmptyAppOptions{})
	require.Nil(t, a.MsgServiceRouter().HandlerByTypeURL("/cosmos.staking.v1beta1.MsgDelegate"))
	require.Nil(t, a.MsgServiceRouter().HandlerByTypeURL("/cosmos.staking.v1beta1.MsgCreateValidator"))
	_, exists := a.ModuleManager.Modules["staking"]
	require.False(t, exists)
	require.Contains(t, a.ModuleManager.Modules, "coreslot")

	emitters := 0
	for _, appModule := range a.ModuleManager.Modules {
		if _, ok := appModule.(module.HasABCIEndBlock); ok {
			emitters++
		}
	}
	require.Equal(t, 1, emitters, "coreslot must be the only validator-update-capable EndBlock module")
}
