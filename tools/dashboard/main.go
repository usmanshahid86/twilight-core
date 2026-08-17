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
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cometbft/cometbft/crypto/tmhash"
	rpchttp "github.com/cometbft/cometbft/rpc/client/http"
	"github.com/cosmos/cosmos-sdk/client"
	sdk "github.com/cosmos/cosmos-sdk/types"
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
	ctx      client.Context
	rewardsQ rewardstypes.QueryClient
	coreQ    coreslottypes.QueryClient
	bankQ    banktypes.QueryClient
}

func main() {
	flag.Parse()

	enc := app.MakeEncodingConfig()
	// depinject's encoding config registers std interfaces but not these custom
	// module Msg types; register them so tx decoding can resolve Any-wrapped
	// rewards/coreslot messages (concrete query responses don't need this).
	rewardstypes.RegisterInterfaces(enc.InterfaceRegistry)
	coreslottypes.RegisterInterfaces(enc.InterfaceRegistry)

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
	mux.HandleFunc("/api/blocks", s.handleReq(s.blocks))
	mux.HandleFunc("/api/block", s.handleReq(s.block))
	mux.HandleFunc("/api/txs", s.handleReq(s.txs))
	mux.HandleFunc("/api/tx", s.handleReq(s.tx))

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

// writeJSON encodes a response body, and swallows the encode error deliberately.
//
// By the time encoding starts the status line and headers are already on the wire,
// so there is no status code left to change and nothing useful to say to the client.
// The error is named and discarded here, once, rather than ignored implicitly at
// four call sites.
func writeJSON(w http.ResponseWriter, body any) {
	_ = json.NewEncoder(w).Encode(body)
}

func (s *server) handle(fn func(context.Context) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		out, err := fn(ctx)
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			writeJSON(w, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, out)
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

// handleReq is the request-aware variant of handle (for endpoints that read query
// params like ?height= / ?hash= / ?limit=).
func (s *server) handleReq(fn func(context.Context, *http.Request) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		out, err := fn(ctx, r)
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			writeJSON(w, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, out)
	}
}

func qint(r *http.Request, key string, def, max int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil || v <= 0 {
		return def
	}
	if v > max {
		return max
	}
	return v
}

// --- block / tx browsing ---------------------------------------------------

type decodedMsg struct {
	Type  string          `json:"type"`            // proto type URL, e.g. /twilight.rewards.v1.MsgClaimRewards
	Body  json.RawMessage `json:"body,omitempty"`  // codec-decoded fields (custom Msgs included)
	Error string          `json:"error,omitempty"` // set if the message JSON marshal failed
}

type decodedTx struct {
	Hash        string       `json:"hash"`
	Height      int64        `json:"height,omitempty"`
	Code        uint32       `json:"code"`
	Success     bool         `json:"success"`
	GasWanted   int64        `json:"gas_wanted,omitempty"`
	GasUsed     int64        `json:"gas_used,omitempty"`
	Memo        string       `json:"memo,omitempty"`
	Messages    []decodedMsg `json:"messages,omitempty"`
	Raw         string       `json:"raw,omitempty"`          // base64 of tx bytes (set when decode fails)
	DecodeError string       `json:"decode_error,omitempty"` // set when the tx itself can't be decoded
}

// decodeTx never returns an error: a tx that can't be decoded degrades to
// {hash, raw, decode_error}, and a message that can't be marshaled degrades to
// {type, error}. One bad tx must never break a block/list response.
func (s *server) decodeTx(txBytes []byte) decodedTx {
	d := decodedTx{Hash: strings.ToUpper(hex.EncodeToString(tmhash.Sum(txBytes)))}
	sdkTx, err := s.ctx.TxConfig.TxDecoder()(txBytes)
	if err != nil {
		d.Raw = base64.StdEncoding.EncodeToString(txBytes)
		d.DecodeError = err.Error()
		return d
	}
	if mt, ok := sdkTx.(interface{ GetMemo() string }); ok {
		d.Memo = mt.GetMemo()
	}
	for _, m := range sdkTx.GetMsgs() {
		dm := decodedMsg{Type: sdk.MsgTypeURL(m)}
		if b, err := s.ctx.Codec.MarshalJSON(m); err != nil {
			dm.Error = err.Error()
		} else {
			dm.Body = json.RawMessage(b)
		}
		d.Messages = append(d.Messages, dm)
	}
	return d
}

// blocks returns the latest N block metas (default 20). Tolerates a short chain.
func (s *server) blocks(ctx context.Context, r *http.Request) (any, error) {
	n := qint(r, "limit", 20, 100)
	st, err := s.ctx.Client.Status(ctx)
	if err != nil {
		return nil, err
	}
	latest := st.SyncInfo.LatestBlockHeight
	min := latest - int64(n) + 1
	if min < 1 {
		min = 1
	}
	out := []map[string]any{}
	info, err := s.ctx.Client.BlockchainInfo(ctx, min, latest)
	if err != nil {
		return map[string]any{"latest": latest, "blocks": out}, nil
	}
	for _, bm := range info.BlockMetas { // newest first
		out = append(out, map[string]any{
			"height":     bm.Header.Height,
			"time":       bm.Header.Time,
			"proposer":   strings.ToUpper(hex.EncodeToString(bm.Header.ProposerAddress)),
			"num_txs":    bm.NumTxs,
			"block_hash": bm.BlockID.Hash.String(),
			"app_hash":   bm.Header.AppHash.String(),
		})
	}
	return map[string]any{"latest": latest, "blocks": out}, nil
}

// block returns one block with its decoded txs (zipped with per-tx results).
func (s *server) block(ctx context.Context, r *http.Request) (any, error) {
	h, err := strconv.ParseInt(r.URL.Query().Get("height"), 10, 64)
	if err != nil || h <= 0 {
		return nil, err
	}
	blk, err := s.ctx.Client.Block(ctx, &h)
	if err != nil {
		return nil, err
	}
	results, _ := s.ctx.Client.BlockResults(ctx, &h) // best-effort for code/gas
	txs := []decodedTx{}
	for i, raw := range blk.Block.Data.Txs {
		dt := s.decodeTx(raw)
		dt.Height = h
		if results != nil && i < len(results.TxsResults) {
			res := results.TxsResults[i]
			dt.Code = res.Code
			dt.Success = res.Code == 0
			dt.GasWanted = res.GasWanted
			dt.GasUsed = res.GasUsed
		}
		txs = append(txs, dt)
	}
	return map[string]any{
		"height":     h,
		"time":       blk.Block.Header.Time,
		"proposer":   strings.ToUpper(hex.EncodeToString(blk.Block.Header.ProposerAddress)),
		"block_hash": blk.BlockID.Hash.String(),
		"app_hash":   blk.Block.Header.AppHash.String(),
		"num_txs":    len(blk.Block.Data.Txs),
		"txs":        txs,
	}, nil
}

// txs scans the recent `heights` (default 50) and returns up to `limit` (default
// 50) decoded txs, newest first. Quiet devnet blocks (0 txs) are skipped cheaply
// via the block metas, so only blocks that actually contain txs are fetched.
func (s *server) txs(ctx context.Context, r *http.Request) (any, error) {
	scan := qint(r, "heights", 50, 500)
	limit := qint(r, "limit", 50, 200)
	st, err := s.ctx.Client.Status(ctx)
	if err != nil {
		return nil, err
	}
	latest := st.SyncInfo.LatestBlockHeight
	out := []decodedTx{}
	// walk newest->oldest in BlockchainInfo chunks (CometBFT caps the range at 20).
	for top := latest; top >= 1 && len(out) < limit && latest-top < int64(scan); top -= 20 {
		bottom := top - 19
		if bottom < 1 {
			bottom = 1
		}
		info, err := s.ctx.Client.BlockchainInfo(ctx, bottom, top)
		if err != nil {
			break
		}
		for _, bm := range info.BlockMetas { // newest first within the chunk
			if bm.NumTxs == 0 {
				continue
			}
			h := bm.Header.Height
			blk, err := s.ctx.Client.Block(ctx, &h)
			if err != nil {
				continue
			}
			results, _ := s.ctx.Client.BlockResults(ctx, &h)
			for i, raw := range blk.Block.Data.Txs {
				dt := s.decodeTx(raw)
				dt.Height = h
				if results != nil && i < len(results.TxsResults) {
					res := results.TxsResults[i]
					dt.Code = res.Code
					dt.Success = res.Code == 0
					dt.GasWanted = res.GasWanted
					dt.GasUsed = res.GasUsed
				}
				out = append(out, dt)
				if len(out) >= limit {
					break
				}
			}
			if len(out) >= limit {
				break
			}
		}
	}
	return map[string]any{"latest": latest, "scanned_heights": scan, "txs": out}, nil
}

// tx looks up a single tx by hash via the RPC tx endpoint.
func (s *server) tx(ctx context.Context, r *http.Request) (any, error) {
	hs := strings.TrimPrefix(strings.TrimSpace(r.URL.Query().Get("hash")), "0x")
	hb, err := hex.DecodeString(hs)
	if err != nil || len(hb) == 0 {
		return nil, err
	}
	res, err := s.ctx.Client.Tx(ctx, hb, false)
	if err != nil {
		return nil, err
	}
	dt := s.decodeTx(res.Tx)
	dt.Height = res.Height
	dt.Code = res.TxResult.Code
	dt.Success = res.TxResult.Code == 0
	dt.GasWanted = res.TxResult.GasWanted
	dt.GasUsed = res.TxResult.GasUsed
	return map[string]any{"tx": dt, "log": res.TxResult.Log}, nil
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
	slots, err := s.coreQ.CoreSlots(ctx, &coreslottypes.QueryCoreSlotsRequest{})
	if err != nil {
		return map[string]any{"by_slot": rows}, nil
	}
	for i, sl := range slots.Slots {
		if i >= *maxSlots {
			break
		}
		r, err := s.rewardsQ.SlotRewards(ctx, &rewardstypes.QuerySlotRewardsRequest{SlotId: sl.SlotId})
		if err != nil {
			continue
		}
		rows = append(rows, s.raw(r))
	}
	return map[string]any{"by_slot": rows}, nil
}
