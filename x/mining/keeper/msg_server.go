package keeper

import (
	"context"

	"github.com/twilight-project/twilight-core/x/mining/types"
)

// The message service is deliberately thin.
//
// Every admission rule, every ceiling and the cache boundary live in the keeper,
// not here. That is what lets the acceptance proofs drive the same code path from
// keeper fixtures and from a signed transaction without the two diverging — a rule
// enforced in the handler wrapper would hold for transactions and silently not
// hold for the keeper entry point that x/mining's own tests and any future
// in-process caller use.
type msgServer struct{ Keeper }

func NewMsgServer(k Keeper) types.MsgServer { return msgServer{Keeper: k} }

func (m msgServer) SubmitSettlementChunk(
	ctx context.Context, msg *types.MsgSubmitSettlementChunk,
) (*types.MsgSubmitSettlementChunkResponse, error) {
	next, err := m.Keeper.SubmitSettlementChunk(ctx, msg)
	if err != nil {
		return nil, err
	}
	return &types.MsgSubmitSettlementChunkResponse{NextChunkIndex: next}, nil
}
