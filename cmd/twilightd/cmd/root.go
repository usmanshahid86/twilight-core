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

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/config"
	"github.com/cosmos/cosmos-sdk/client/debug"
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
)

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

	basicManager := module.NewBasicManager(
		auth.AppModuleBasic{},
		bank.AppModuleBasic{},
		consensus.AppModuleBasic{},
		coreslot.NewAppModuleBasic(app.AuthorityAddress(), app.EmergencyAuthorityAddress()),
	)

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
		rpc.QueryEventForTxCmd(),
		server.QueryBlockCmd(),
		server.QueryBlocksCmd(),
		authcmd.QueryTxsByEventsCmd(),
		authcmd.QueryTxCmd(),
		authcmd.GetSignCommand(),
		authcmd.GetBroadcastCommand(),
		authcmd.GetEncodeCommand(),
		authcmd.GetDecodeCommand(),
		keys.Commands(),
		coreslotcli.GetTxCmd(),
		coreslotcli.GetQueryCmd(),
		coreslotcli.GetGenesisCmd(),
	)
	server.AddCommandsWithStartCmdOptions(root, app.DefaultNodeHome, newApp, appExport, server.StartCmdOptions{})
	return root
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
