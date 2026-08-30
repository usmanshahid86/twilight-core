package cmd

import (
	"errors"
	"io"
	"os"
	"time"

	cmtcfg "github.com/cometbft/cometbft/config"
	cmttypes "github.com/cometbft/cometbft/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"cosmossdk.io/log"
	"cosmossdk.io/x/upgrade"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/config"
	"github.com/cosmos/cosmos-sdk/client/debug"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/client/keys"
	"github.com/cosmos/cosmos-sdk/client/pruning"
	"github.com/cosmos/cosmos-sdk/client/rpc"
	"github.com/cosmos/cosmos-sdk/client/snapshot"
	"github.com/cosmos/cosmos-sdk/server"
	serverconfig "github.com/cosmos/cosmos-sdk/server/config"
	servertypes "github.com/cosmos/cosmos-sdk/server/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	auth "github.com/cosmos/cosmos-sdk/x/auth"
	authcmd "github.com/cosmos/cosmos-sdk/x/auth/client/cli"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	bank "github.com/cosmos/cosmos-sdk/x/bank"
	consensus "github.com/cosmos/cosmos-sdk/x/consensus"
	genutilcli "github.com/cosmos/cosmos-sdk/x/genutil/client/cli"

	"github.com/twilight-project/twilight-core/app"
	"github.com/twilight-project/twilight-core/x/coreslot"
	coreslotcli "github.com/twilight-project/twilight-core/x/coreslot/client/cli"
	"github.com/twilight-project/twilight-core/x/mining"
	miningcli "github.com/twilight-project/twilight-core/x/mining/client/cli"
	"github.com/twilight-project/twilight-core/x/rewards"
	rewardscli "github.com/twilight-project/twilight-core/x/rewards/client/cli"
	rewardstypes "github.com/twilight-project/twilight-core/x/rewards/types"
)

// BasicManager returns the module basics that `twilightd init` uses to write a
// genesis document.
//
// Every module the app mounts a store for MUST appear here. A module that is
// missing produces a genesis with no section for it, and the SDK's module manager
// SKIPS InitGenesis for a module whose genesis data is absent rather than failing
// — so the store is mounted, never written, and its IAVL tree ends up with no
// versions at all. The chain then still produces blocks, but every historical
// state read fails for EVERY module, because loading a version requires every
// mounted store to have it.
//
// That failure is silent at startup and only appears as unservable queries, which
// is why the correspondence is asserted by a test rather than left to review.
func BasicManager() module.BasicManager {
	return module.NewBasicManager(
		auth.AppModuleBasic{},
		bank.AppModuleBasic{},
		consensus.AppModuleBasic{},
		coreslot.NewAppModuleBasic(app.AuthorityAddress(), app.EmergencyAuthorityAddress()),
		// rewards basic module: codec/genesis/interface registration for `init`.
		// Tx/query CLI commands are deferred to Phase 9.
		rewards.NewAppModuleBasic(),
		mining.NewAppModuleBasic(),
		// x/upgrade mounts a store, so `twilightd init` must write its genesis
		// section. A module the node initializes but the CLI omits produces a
		// genesis whose store is never written.
		//
		// The zero value leaves the module's address codec nil, and there is no
		// exported constructor in x/upgrade v0.2.0 to supply one — the field is
		// unexported. That is safe only because this manager is used for genesis
		// alone (InitCmd and DefaultGenesis); AppModuleBasic.GetTxCmd would
		// dereference the nil codec, so do not pass this manager to
		// AddTxCommands.
		upgrade.AppModuleBasic{},
	)
}

// MempoolBacklogBlocks is the number of blocks' worth of transaction bytes a
// node will queue in its mempool before it refuses more.
//
// # What this bounds
//
// The DEPTH OF THE QUEUE a single sender can occupy. Transactions on this chain
// carry no fee and the mempool is FIFO with no per-sender fairness, so whoever
// fills the queue first is served first, and the queue's depth is exactly the
// window during which an honest transaction waits behind somebody else's
// backlog. Bounding the depth bounds that wait; it does not divide the queue
// fairly, and nothing here claims it does.
//
// CometBFT's default max_txs_bytes is 1 GiB (1073741824 bytes) against a
// default block max_bytes of 21 MiB (22020096 bytes, from
// cmttypes.DefaultConsensusParams, which is also what `twilightd init` writes
// into genesis). That is 48.76 blocks of backlog. A few blocks is enough to
// absorb a burst that arrives faster than one block drains, including across a
// proposer change; tens of blocks is a queue an operator would experience as an
// outage. Four is a judgment inside "a few" rather than a measured optimum, and
// being configuration it costs nothing to revise.
//
// # Why mempool.size is deliberately left at its default
//
// The transaction COUNT cap stays at CometBFT's default 5000 because it already
// binds far tighter than any byte cap for ordinary traffic. A signed
// single-MsgSend transaction built with this chain's own tx config measures 310
// bytes, so 5000 of them are 1550000 bytes — about seven hundredths of one
// block's data budget (22018921 bytes at a four-validator set). Lowering the
// count would shrink the honest burst a node can hold without touching the case
// this addresses.
//
// The byte cap is what does the work, and it does it in the regime the count
// cap leaves open: max_tx_bytes permits 1 MiB per transaction, so 5000
// transactions may be 4.88 GiB. The two caps are complementary — the count
// bounds small-transaction backlog, the bytes bound large-transaction backlog —
// which is why only one of them is changed here.
//
// # What this does NOT do
//
//   - It is NODE-LOCAL configuration. A proposer running different settings is
//     not bound by it, so this constrains an operator's own node and nothing
//     else. That is tolerable only because every proposer here is an admitted
//     CoreSlot operator, which makes a non-conforming proposer an operational
//     problem rather than an anonymous one.
//   - It gives NO consensus-enforced per-sender fairness and NO economic bound.
//     The per-block work ceiling is TW-004's finite max_gas, which is not
//     ratified here.
//   - TW-005 is MITIGATED, NOT CLOSED, and #147 stays open.
//   - It reaches only NEWLY INITIALIZED nodes. The SDK writes config.toml from
//     this value only when the file is absent; a node whose config.toml already
//     exists keeps whatever it was given, and needs an operator to edit it.
const MempoolBacklogBlocks = 4

// nodeConfig is the CometBFT node configuration this binary hands the SDK, which
// writes it into config.toml when a node has none yet.
//
// The mempool byte bound is derived from the same default block size that
// `twilightd init` writes into the genesis consensus params, so the two cannot
// silently drift apart at the default.
//
// They CAN drift when a network launches with a block max_bytes other than the
// default, because a node's config.toml is written once and does not follow
// genesis. That is the case to watch: applying a ratified block parameter means
// launching a network with it, which leaves every carried-over config.toml
// describing the previous one.
//
// It cannot drift by a live parameter change, because this chain has no way to
// make one. x/consensus is configured with the authority module account, which
// is keyless, and no module proxies its MsgUpdateParams the way x/coreslot
// proxies x/upgrade — so no transaction can reach it on a running chain. That
// gap is recorded in #167 and is not addressed here.
func nodeConfig() *cmtcfg.Config {
	cfg := cmtcfg.DefaultConfig()
	cfg.Mempool.MaxTxsBytes = MempoolBacklogBlocks * cmttypes.DefaultConsensusParams().Block.MaxBytes
	cfg.Consensus.TimeoutCommit = targetBlockInterval()
	return cfg
}

// targetBlockInterval is the commit timeout a node runs with, derived from the
// block time the reward schedule is written against.
//
// # Why block pacing is an economic value
//
// Emission is initial_block_subsidy PER BLOCK, and nothing in the state machine
// reads a clock: rewards params carry target_block_time_seconds, but it is
// validated non-zero and then used in no computation at all. So the WALL-CLOCK
// emission rate is decided entirely by how fast blocks are produced, which is
// node-local configuration rather than a consensus rule.
//
// At the shipped max_supply and subsidy, 5-second blocks emit ~12.5% of supply in
// the first year, with the first halving near four years. 1-second blocks reach
// that halving in about 9.6 months, so the first year emits roughly 56% — NOT the
// 62.5% that five times the annualized rate suggests, because the rate halves for
// the remainder of the year once the threshold is crossed. Any projection that
// multiplies a per-block subsidy by a year of blocks is a PRE-HALVING rate, and
// only equals the year's emission while the first halving falls outside it.
//
// # This is deliberately NOT a behavior change
//
// The SDK already produces 5 seconds. InterceptConfigsPreRunHandler overrides
// TimeoutCommit to 5s whenever the config it is handed still holds CometBFT's
// default of 1s — "the SDK is opinionated about those comet values"
// (server/util.go). A fresh `twilightd init` therefore already wrote 5s before
// this existed, and removing this line today changes nothing an operator sees.
//
// What it changes is WHERE the value comes from. Without it, the block pacing that
// sets this chain's monetary schedule is an SDK implementation detail that happens
// to coincide with DefaultTargetBlockTimeSeconds, and a future SDK revising its
// opinion would move the emission rate with no local signal. Setting it here takes
// ownership, and the SDK's override no longer applies because the value it guards
// on is no longer the CometBFT default.
//
// # The limit of that coupling, stated precisely
//
// This binds the node's pacing to the Go DEFAULT constant, NOT to the chain's
// configured target_block_time_seconds. It cannot bind to the configured value:
// this runs in PersistentPreRunE, before any genesis file exists to read. So a
// network launched with target_block_time_seconds = 10 still paces at 5s, and no
// consensus rule notices — that field drives no computation anywhere.
//
// Nothing here closes the genesis side of that gap: no check in this repository
// compares a genesis-declared target_block_time_seconds against the pacing nodes
// actually run. A genesis verifier doing exactly that is proposed in #171 and is
// not merged, so the gap is open rather than covered.
//
// The test asserts the OUTCOME rather than this line. What that detects, precisely:
// it fails when the pacing a node writes DIVERGES from the reward default — an SDK
// opinion moving alone, a CometBFT default moving alone, this helper changed alone.
// It does NOT fail when the reward default itself moves, because helper and
// expectation both follow it. That case is a deliberate economic change and belongs
// under review, not under a test that would only restate an identity.
//
// # What it is NOT
//
// timeout_commit is the dominant term in block interval, not the whole of it: a
// block also costs the time to propose and vote, so real intervals sit slightly
// above this. Block time stays an operational property rather than a protocol
// guarantee — which is exactly why epoch length is denominated in BLOCKS.
//
// It is also node-local, and its reach across EXISTING homes is narrower than
// "fresh nodes only" but not nil — worth stating exactly, because the obvious
// summary is wrong.
//
// The SDK writes a config.toml only when none exists. When one does, it unmarshals
// that file ONTO the configuration supplied here, so a key the file SETS wins and a
// key it OMITS inherits this value:
//
//	existing file says timeout_commit = "3s"  ->  3s, before and after
//	existing file omits timeout_commit        ->  1s before, 5s after
//
// The second row is a real behavior change on an existing home, and it is the
// intended direction: 1 second was never the pacing this chain's reward schedule
// is written against. A node that wants something else states it, and is obeyed.
func targetBlockInterval() time.Duration {
	return time.Duration(rewardstypes.DefaultTargetBlockTimeSeconds) * time.Second
}

func NewRootCmd() *cobra.Command {
	encoding := app.MakeEncodingConfig()
	clientCtx := client.Context{}.
		WithCodec(encoding.Codec).
		WithInterfaceRegistry(encoding.InterfaceRegistry).
		WithLegacyAmino(encoding.Amino).
		WithTxConfig(encoding.TxConfig).
		WithInput(os.Stdin).
		WithAccountRetriever(authtypes.AccountRetriever{}).
		WithHomeDir(app.DefaultNodeHome).
		WithViper(app.Name)

	basicManager := BasicManager()

	root := &cobra.Command{
		Use: app.Name + "d", Short: "Twilight Proof-of-Authority node", SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SetOut(cmd.OutOrStdout())
			cmd.SetErr(cmd.ErrOrStderr())
			var err error
			clientCtx = clientCtx.WithCmdContext(cmd.Context()).WithViper(app.Name)
			clientCtx, err = client.ReadPersistentCommandFlags(clientCtx, cmd.Flags())
			if err != nil {
				return err
			}
			clientCtx, err = config.ReadFromClientConfig(clientCtx)
			if err != nil {
				return err
			}
			if err := client.SetCmdClientContextHandler(clientCtx, cmd); err != nil {
				return err
			}
			srvCfg := serverconfig.DefaultConfig()
			srvCfg.MinGasPrices = "0" + app.BaseDenom
			return server.InterceptConfigsPreRunHandler(cmd, serverconfig.DefaultConfigTemplate, srvCfg, nodeConfig())
		},
	}
	sdk.GetConfig().Seal()
	root.AddCommand(
		genutilcli.InitCmd(basicManager, app.DefaultNodeHome),
		genutilcli.AddGenesisAccountCmd(app.DefaultNodeHome, encoding.InterfaceRegistry.SigningContext().AddressCodec()),
		// Validates the WHOLE document by running every module's ValidateGenesis.
		// Without it the only genesis check on the binary was
		// `coreslot-genesis validate`, which inspects one module's state and says
		// nothing about the rest — so an assembly tool could report success and
		// leave a genesis that fails at `start`, which is exactly what happened.
		genutilcli.ValidateGenesisCmd(basicManager),
		debug.Cmd(),
		pruning.Cmd(newApp, app.DefaultNodeHome),
		snapshot.Cmd(newApp),
		queryCommand(),
		txCommand(),
		keys.Commands(),
		// Custom modules keep their bespoke top-level commands (coreslot register,
		// coreslot-query, rewards, rewards-query) for tooling compatibility.
		coreslotcli.GetTxCmd(),
		coreslotcli.GetQueryCmd(),
		coreslotcli.GetGenesisCmd(),
		rewardscli.GetTxCmd(),
		rewardscli.GetQueryCmd(),
		miningcli.GetQueryCmd(),
	)
	server.AddCommandsWithStartCmdOptions(root, app.DefaultNodeHome, newApp, appExport, server.StartCmdOptions{})

	// Wire AutoCLI: generate the standard `tx`/`query` module command trees (bank,
	// auth, consensus, ...) onto the parents added above. A throwaway in-memory app
	// yields the module metadata; app.New ignores AppOptions (nil ok) and
	// loadLatest=false avoids touching any state.
	tempApp := app.New(log.NewNopLogger(), dbm.NewMemDB(), nil, false, nil)
	if err := tempApp.AutoCliOpts().EnhanceRootCommand(root); err != nil {
		panic(err)
	}
	return root
}

// queryCommand assembles the standard `query` parent; AutoCLI adds per-module
// query subcommands (e.g. `query bank balances`) onto it.
func queryCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        "query",
		Aliases:                    []string{"q"},
		Short:                      "Querying subcommands",
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}
	cmd.AddCommand(
		rpc.QueryEventForTxCmd(),
		server.QueryBlockCmd(),
		server.QueryBlocksCmd(),
		authcmd.QueryTxsByEventsCmd(),
		authcmd.QueryTxCmd(),
	)
	cmd.PersistentFlags().String(flags.FlagNode, "tcp://localhost:26657", "<host>:<port> to CometBFT RPC interface for this chain")
	return cmd
}

// txCommand assembles the standard `tx` parent; AutoCLI adds per-module tx
// subcommands (e.g. `tx bank send`) onto it.
func txCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        "tx",
		Short:                      "Transactions subcommands",
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}
	cmd.AddCommand(
		authcmd.GetSignCommand(),
		authcmd.GetSignBatchCommand(),
		authcmd.GetMultiSignCommand(),
		authcmd.GetValidateSignaturesCommand(),
		authcmd.GetBroadcastCommand(),
		authcmd.GetEncodeCommand(),
		authcmd.GetDecodeCommand(),
	)
	cmd.PersistentFlags().String(flags.FlagNode, "tcp://localhost:26657", "<host>:<port> to CometBFT RPC interface for this chain")
	return cmd
}

func newApp(logger log.Logger, db dbm.DB, traceStore io.Writer, opts servertypes.AppOptions) servertypes.Application {
	return app.New(logger, db, traceStore, true, opts, server.DefaultBaseappOptions(opts)...)
}

func appExport(logger log.Logger, db dbm.DB, traceStore io.Writer, height int64, forZeroHeight bool, jailAllowed []string, opts servertypes.AppOptions, modules []string) (servertypes.ExportedApp, error) {
	v, ok := opts.(*viper.Viper)
	if !ok {
		return servertypes.ExportedApp{}, errors.New("app options are not viper")
	}
	v.Set(server.FlagInvCheckPeriod, 1)
	a := app.New(logger, db, traceStore, height == -1, v, server.DefaultBaseappOptions(v)...)
	if height != -1 {
		if err := a.LoadHeight(height); err != nil {
			return servertypes.ExportedApp{}, err
		}
	}
	return a.ExportAppStateAndValidators(forZeroHeight, jailAllowed, modules)
}
