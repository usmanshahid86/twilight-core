package x

import types "example.com/m/x/rewards/types"

func f(k K) {
	send := k.bank.SendCoinsFromModuleToAccount
	send(ctx, types.ModuleName, to, amt)
}
