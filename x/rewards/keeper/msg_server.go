package keeper

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/x/rewards/types"
)

type msgServer struct{ Keeper }

func NewMsgServer(k Keeper) types.MsgServer { return msgServer{Keeper: k} }

func (m msgServer) UpdateRewardsParams(ctx context.Context, msg *types.MsgUpdateRewardsParams) (*types.MsgUpdateRewardsParamsResponse, error) {
	if msg == nil || msg.Params == nil {
		return nil, types.ErrInvalidParams.Wrap("params are required")
	}
	authority, err := m.coreSlotKeeper.GetAuthority(ctx)
	if err != nil {
		return nil, err
	}
	if msg.Authority != authority {
		return nil, types.ErrInvalidParams.Wrap("unauthorized rewards params update")
	}
	current, err := m.GetParams(ctx)
	if err != nil {
		return nil, err
	}
	next := *msg.Params
	// The retired pause switches carry no authority, but they must still not drift
	// through this path: a params document that flipped one would read as though
	// the chain had a second pause authority beside the canonical state.
	if next.EmissionsEnabled != current.EmissionsEnabled ||
		next.EpochSettlementEnabled != current.EpochSettlementEnabled ||
		next.ClaimsEnabled != current.ClaimsEnabled {
		return nil, types.ErrInvalidParams.Wrap(
			"the deprecated pause flags are not authoritative and may not be changed; use the canonical pause transactions")
	}
	if err := m.SetPendingParams(ctx, next); err != nil {
		return nil, err
	}
	emitRewardsEvent(ctx, types.EventTypeParamsUpdateQueued,
		sdk.NewAttribute(types.AttributeKeyAuthority, msg.Authority),
	)
	return &types.MsgUpdateRewardsParamsResponse{}, nil
}

// PauseRewards schedules a global rewards pause for H+1.
//
// The three selector fields on the message are deprecated and ignored: a pause is
// global. Honoring them would preserve exactly the partial-pause semantics §16
// retired, and would let a caller pause accrual while leaving release enabled.
func (m msgServer) PauseRewards(ctx context.Context, msg *types.MsgPauseRewards) (*types.MsgPauseRewardsResponse, error) {
	if msg == nil {
		return nil, types.ErrInvalidParams.Wrap("pause request is required")
	}
	if err := m.schedulePause(ctx, msg.EmergencyAuthority, true); err != nil {
		return nil, err
	}
	return &types.MsgPauseRewardsResponse{}, nil
}

// ResumeRewards schedules a global rewards resume for H+1.
func (m msgServer) ResumeRewards(ctx context.Context, msg *types.MsgResumeRewards) (*types.MsgResumeRewardsResponse, error) {
	if msg == nil {
		return nil, types.ErrInvalidParams.Wrap("resume request is required")
	}
	if err := m.schedulePause(ctx, msg.EmergencyAuthority, false); err != nil {
		return nil, err
	}
	return &types.MsgResumeRewardsResponse{}, nil
}

// schedulePause authorizes and records a pause transition for the next block.
//
// The transition is scheduled, never applied here. A value applied mid-block
// would make a block's reward treatment depend on where in the block the
// transaction landed; scheduling it for H+1 makes it a property of the block.
func (m msgServer) schedulePause(ctx context.Context, signer string, paused bool) error {
	authority, err := m.coreSlotKeeper.GetEmergencyAuthority(ctx)
	if err != nil {
		return err
	}
	if signer != authority {
		return types.ErrInvalidParams.Wrap("unauthorized rewards emergency action")
	}
	height := sdk.UnwrapSDKContext(ctx).BlockHeight()
	if err := m.SchedulePauseTransition(ctx, height, paused); err != nil {
		return err
	}
	eventType := types.EventTypePaused
	if !paused {
		eventType = types.EventTypeResumed
	}
	emitRewardsEvent(ctx, eventType, sdk.NewAttribute(types.AttributeKeyAuthority, signer))
	return nil
}
