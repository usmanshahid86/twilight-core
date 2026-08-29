package caller

import types "github.com/twilight-project/twilight-core/internal/payoutledger/testdata/implicit_const/types"

func f(k K) { k.bank.SendCoinsFromModuleToAccount(ctx, types.ModuleName, to, amt) }
