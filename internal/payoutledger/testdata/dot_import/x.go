package x

import . "example.com/m/x/rewards/types"

func f(k K) { k.bank.SendCoinsFromModuleToAccount(ctx, ModuleName, to, amt) }
