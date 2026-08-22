package cmd

import (
	"errors"
	"io"
	"os"

	cmtcfg "github.com/cometbft/cometbft/config"
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
		upgrade.AppModuleBasic{},
	)
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
			return server.InterceptConfigsPreRunHandler(cmd, serverconfig.DefaultConfigTemplate, srvCfg, cmtcfg.DefaultConfig())
		},
	}
	sdk.GetConfig().Seal()
	root.AddCommand(
		genutilcli.InitCmd(basicManager, app.DefaultNodeHome),
		genutilcli.AddGenesisAccountCmd(app.DefaultNodeHome, encoding.InterfaceRegistry.SigningContext().AddressCodec()),
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
