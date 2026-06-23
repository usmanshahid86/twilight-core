// Command twilight-dashboard is a small read-only web dashboard for the Twilight
// devnet. It reuses the chain's generated query clients over the public CometBFT
// RPC (no gRPC/REST ports required) to decode the custom x/rewards and x/coreslot
// state, and serves it as plain JSON to an embedded static frontend.
//
// It is isolated from the chain binary: build with `go build ./tools/dashboard`.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"time"

	rpchttp "github.com/cometbft/cometbft/rpc/client/http"
	"github.com/cosmos/cosmos-sdk/client"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	gogoproto "github.com/cosmos/gogoproto/proto"

	"github.com/twilight-project/twilight-core/app"
	coreslottypes "github.com/twilight-project/twilight-core/x/coreslot/types"
	rewardstypes "github.com/twilight-project/twilight-core/x/rewards/types"
)

//go:embed web
var webFS embed.FS

var (
	rpcAddr  = flag.String("rpc", "http://localhost:26657", "CometBFT RPC of the devnet node")
	listen   = flag.String("listen", ":8080", "address to serve the dashboard on")
	denom    = flag.String("denom", "utwlt", "accounting denom")
	maxSlots = flag.Int("max-slots", 64, "max slots/claim-rows to scan")
)

type server struct {
	ctx    client.Context
	rewardsQ rewardstypes.QueryClient
	coreQ  coreslottypes.QueryClient
	bankQ  banktypes.QueryClient
}

func main() {
	flag.Parse()

	enc := app.MakeEncodingConfig()
	rpc, err := rpchttp.New(*rpcAddr, "/websocket")
	if err != nil {
		log.Fatalf("rpc: %v", err)
	}
	cctx := client.Context{}.
		WithClient(rpc).
		WithCodec(enc.Codec).
		WithInterfaceRegistry(enc.InterfaceRegistry).
		WithTxConfig(enc.TxConfig)

	s := &server{
		ctx:      cctx,
		rewardsQ: rewardstypes.NewQueryClient(cctx),
		coreQ:    coreslottypes.NewQueryClient(cctx),
		bankQ:    banktypes.NewQueryClient(cctx),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/overview", s.handle(s.overview))
	mux.HandleFunc("/api/params", s.handle(s.params))
	mux.HandleFunc("/api/epochs", s.handle(s.epochs))
	mux.HandleFunc("/api/validators", s.handle(s.validators))
	mux.HandleFunc("/api/claims", s.handle(s.claims))

	sub, _ := fs.Sub(webFS, "web")
	mux.Handle("/", http.FileServer(http.FS(sub)))

	log.Printf("twilight-dashboard: serving %s -> rpc %s", *listen, *rpcAddr)
	log.Fatal(http.ListenAndServe(*listen, cors(mux)))
}

// --- helpers ---------------------------------------------------------------

func cors(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		h.ServeHTTP(w, r)
	})
}

func (s *server) handle(fn func(context.Context) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		out, err := fn(ctx)
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(out)
	}
}

// raw marshals a proto message through the chain codec so every custom field is
// rendered as JSON.
func (s *server) raw(m gogoproto.Message) json.RawMessage {
	b, err := s.ctx.Codec.MarshalJSON(m)
	if err != nil {
		return json.RawMessage(`null`)
	}
	return json.RawMessage(b)
}

// --- endpoints -------------------------------------------------------------

func (s *server) overview(ctx context.Context) (any, error) {
	out := map[string]any{}

	if st, err := s.ctx.Client.Status(ctx); err == nil {
		out["chain_id"] = st.NodeInfo.Network
		out["height"] = st.SyncInfo.LatestBlockHeight
		out["block_time"] = st.SyncInfo.LatestBlockTime
		out["catching_up"] = st.SyncInfo.CatchingUp
	}
	if r, err := s.rewardsQ.EpochInfo(ctx, &rewardstypes.QueryEpochInfoRequest{}); err == nil {
		out["epoch"] = s.raw(r)
	}
	if r, err := s.rewardsQ.CumulativeEmitted(ctx, &rewardstypes.QueryCumulativeEmittedRequest{}); err == nil {
		out["cumulative"] = s.raw(r)
	}
	if r, err := s.rewardsQ.ModuleBalances(ctx, &rewardstypes.QueryModuleBalancesRequest{}); err == nil {
		out["module_balances"] = s.raw(r)
	}
	if r, err := s.bankQ.SupplyOf(ctx, &banktypes.QuerySupplyOfRequest{Denom: *denom}); err == nil {
		out["supply"] = s.raw(r)
	}
	if r, err := s.coreQ.ActiveCoreSlots(ctx, &coreslottypes.QueryActiveCoreSlotsRequest{}); err == nil {
		out["active_slots"] = s.raw(r)
	}
	return out, nil
}

func (s *server) params(ctx context.Context) (any, error) {
	out := map[string]any{}
	if r, err := s.rewardsQ.Params(ctx, &rewardstypes.QueryParamsRequest{}); err == nil {
		out["rewards"] = s.raw(r)
	}
	if r, err := s.coreQ.Params(ctx, &coreslottypes.QueryParamsRequest{}); err == nil {
		out["coreslot"] = s.raw(r)
	}
	if r, err := s.rewardsQ.NextHalving(ctx, &rewardstypes.QueryNextHalvingRequest{}); err == nil {
		out["next_halving"] = s.raw(r)
	}
	if r, err := s.rewardsQ.SupplySchedule(ctx, &rewardstypes.QuerySupplyScheduleRequest{}); err == nil {
		out["supply_schedule"] = s.raw(r)
	}
	return out, nil
}

func (s *server) epochs(ctx context.Context) (any, error) {
	out := map[string]any{}
	if r, err := s.rewardsQ.EpochInfo(ctx, &rewardstypes.QueryEpochInfoRequest{}); err == nil {
		out["info"] = s.raw(r)
	}
	if r, err := s.rewardsQ.CurrentEpochActiveBlocks(ctx, &rewardstypes.QueryCurrentEpochActiveBlocksRequest{}); err == nil {
		out["current_active_blocks"] = s.raw(r)
	}
	return out, nil
}

func (s *server) validators(ctx context.Context) (any, error) {
	out := map[string]any{}
	if r, err := s.coreQ.CoreSlots(ctx, &coreslottypes.QueryCoreSlotsRequest{}); err == nil {
		out["slots"] = s.raw(r)
	}
	if r, err := s.coreQ.ActiveCoreSlots(ctx, &coreslottypes.QueryActiveCoreSlotsRequest{}); err == nil {
		out["active"] = s.raw(r)
	}
	if v, err := s.ctx.Client.Validators(ctx, nil, nil, nil); err == nil {
		out["cometbft_total"] = v.Total
	}
	return out, nil
}

func (s *server) claims(ctx context.Context) (any, error) {
	rows := []json.RawMessage{}
	for slot := uint64(1); int(slot) <= *maxSlots; slot++ {
		r, err := s.rewardsQ.SlotRewards(ctx, &rewardstypes.QuerySlotRewardsRequest{SlotId: slot})
		if err != nil || r == nil {
			break
		}
		rows = append(rows, s.raw(r))
	}
	return map[string]any{"by_slot": rows}, nil
}
