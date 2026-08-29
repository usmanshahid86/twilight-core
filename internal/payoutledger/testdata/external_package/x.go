package x

import authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

func f(k K) { k.bank.SendCoinsFromModuleToAccount(ctx, authtypes.ModuleName, to, amt) }
