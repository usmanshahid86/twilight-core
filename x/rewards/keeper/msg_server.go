package keeper

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/x/rewards/types"
)

type msgServer struct{ Keeper }

func NewMsgServer(k Keeper) types.MsgServer { return msgServer{Keeper: k} }

func (m msgServer) ClaimRewards(ctx context.Context, msg *types.MsgClaimRewards) (*types.MsgClaimRewardsResponse, error) {
	if err := m.Keeper.ClaimRewards(ctx, msg); err != nil {
		return nil, err
	}
	return &types.MsgClaimRewardsResponse{}, nil
}

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
	if next.EmissionsEnabled != current.EmissionsEnabled ||
		next.EpochSettlementEnabled != current.EpochSettlementEnabled ||
		next.ClaimsEnabled != current.ClaimsEnabled {
		return nil, types.ErrInvalidParams.Wrap("pause flags may only be changed by emergency authority")
	}
	if err := m.SetPendingParams(ctx, next); err != nil {
		return nil, err
	}
	emitRewardsEvent(ctx, types.EventTypeParamsUpdateQueued,
		sdk.NewAttribute(types.AttributeKeyAuthority, msg.Authority),
	)
	return &types.MsgUpdateRewardsParamsResponse{}, nil
}

func (m msgServer) PauseRewards(ctx context.Context, msg *types.MsgPauseRewards) (*types.MsgPauseRewardsResponse, error) {
	if msg == nil {
		return nil, types.ErrInvalidParams.Wrap("pause request is required")
	}
	if err := m.setEmergencyFlags(ctx, msg.EmergencyAuthority, false, msg.PauseEmissions, msg.PauseEpochSettlement, msg.PauseClaims); err != nil {
		return nil, err
	}
	return &types.MsgPauseRewardsResponse{}, nil
}

func (m msgServer) ResumeRewards(ctx context.Context, msg *types.MsgResumeRewards) (*types.MsgResumeRewardsResponse, error) {
	if msg == nil {
		return nil, types.ErrInvalidParams.Wrap("resume request is required")
	}
	if err := m.setEmergencyFlags(ctx, msg.EmergencyAuthority, true, msg.ResumeEmissions, msg.ResumeEpochSettlement, msg.ResumeClaims); err != nil {
		return nil, err
	}
	return &types.MsgResumeRewardsResponse{}, nil
}

func (m msgServer) setEmergencyFlags(ctx context.Context, signer string, enabled, emissions, settlement, claims bool) error {
	authority, err := m.coreSlotKeeper.GetEmergencyAuthority(ctx)
	if err != nil {
		return err
	}
	if signer != authority {
		return types.ErrInvalidParams.Wrap("unauthorized rewards emergency action")
	}
	params, err := m.GetParams(ctx)
	if err != nil {
		return err
	}
	if emissions {
		params.EmissionsEnabled = enabled
	}
	if settlement {
		params.EpochSettlementEnabled = enabled
	}
	if claims {
		params.ClaimsEnabled = enabled
	}
	if err := m.SetParams(ctx, params); err != nil {
		return err
	}
	eventType := types.EventTypePaused
	if enabled {
		eventType = types.EventTypeResumed
	}
	emitRewardsEvent(ctx, eventType, sdk.NewAttribute(types.AttributeKeyAuthority, signer))
	return nil
}
