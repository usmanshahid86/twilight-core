package cmd_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	cmtcfg "github.com/cometbft/cometbft/config"
	cmttypes "github.com/cometbft/cometbft/types"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"

	svrcmd "github.com/cosmos/cosmos-sdk/server/cmd"

	"github.com/twilight-project/twilight-core/cmd/twilightd/cmd"
	rewardstypes "github.com/twilight-project/twilight-core/x/rewards/types"
)

// The mempool bound is asserted against the config.toml the COMMAND WRITES, not
// against the constant that produced it.
//
// Reading MempoolBacklogBlocks back out would restate an arithmetic identity,
// and it would keep passing if the customized node configuration were never
// handed to the SDK at all — which is the failure this exists to catch. That
// customization is one argument of one call in PersistentPreRunE; restoring the
// pass-through default is a one-token edit that compiles, changes no other
// behavior, and is invisible to every other test here.
//
// So this drives the real command tree against a throwaway home directory, lets
// the real pre-run handler and the real `init` write the real files, and reads
// the values back off disk. What it proves is the whole chain of custody:
// constant -> node configuration -> SDK -> config.toml on an operator's disk.
func TestInitWritesCustomizedNodeConfig(t *testing.T) {
	home := t.TempDir()

	root := cmd.NewRootCmd()
	// `init` takes its configuration from the server context POINTER that the
	// binary's entry point seeds into the command context, and falls back to
	// stock CometBFT defaults when that pointer is absent — silently, because a
	// missing context is not an error. A test that skipped this seeding would
	// therefore assert against defaults and could never fail, so it is done the
	// same way `main` does it rather than by hand.
	root.SetContext(svrcmd.CreateExecuteContext(context.Background()))
	root.SetArgs([]string{"init", "mempool-bounds-check", "--chain-id", "twilight-test-1", "--home", home})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)

	// `init` prints the whole genesis document straight to the process's stderr,
	// past cobra's writers, which would put a few hundred lines of JSON into
	// every CI run of the package. It is captured to a file rather than a pipe so
	// a large or unexpected write cannot block, and replayed if `init` fails so
	// nothing diagnostic is lost.
	stderrLog := filepath.Join(t.TempDir(), "init-stderr.txt")
	capture, err := os.Create(stderrLog)
	require.NoError(t, err)
	realStderr := os.Stderr
	os.Stderr = capture
	execErr := root.Execute()
	os.Stderr = realStderr
	require.NoError(t, capture.Close())
	if execErr != nil {
		spill, _ := os.ReadFile(stderrLog)
		require.NoError(t, execErr, "`init` must succeed for anything below to mean something\n%s", spill)
	}

	cfgFile := filepath.Join(home, "config", "config.toml")
	require.FileExists(t, cfgFile, "the node must have been given a config.toml")
	written := readCometConfig(t, cfgFile)

	defaults := cmtcfg.DefaultConfig()
	blockMaxBytes := cmttypes.DefaultConsensusParams().Block.MaxBytes

	t.Run("the queue holds a few blocks of backlog, not tens", func(t *testing.T) {
		// The posture being replaced: 1 GiB against a 21 MiB block is 48 blocks
		// of backlog, and with no per-sender fairness that depth is the window a
		// single sender can occupy.
		require.Equal(t, int64(1073741824), defaults.Mempool.MaxTxsBytes,
			"the upstream default this bound exists to replace has moved; re-derive the multiple before touching this")
		require.Equal(t, int64(22020096), blockMaxBytes,
			"the default block size the bound is derived from has moved; re-derive the multiple")

		require.NotEqual(t, defaults.Mempool.MaxTxsBytes, written.Mempool.MaxTxsBytes,
			"config.toml carries the upstream default — the customized node configuration never reached the SDK")
		require.Equal(t, cmd.MempoolBacklogBlocks*blockMaxBytes, written.Mempool.MaxTxsBytes)

		// Held as the property rather than the number, so a later change to the
		// multiple is still answerable to "a few blocks".
		blocks := written.Mempool.MaxTxsBytes / blockMaxBytes
		require.GreaterOrEqual(t, blocks, int64(2),
			"below two blocks the queue cannot absorb a burst that spans a proposer change")
		require.LessOrEqual(t, blocks, int64(8),
			"this depth IS the starvation window, so it has to stay in the low single digits")

		// A queue that cannot hold one maximum-size transaction rejects traffic
		// the chain otherwise accepts.
		require.Greater(t, written.Mempool.MaxTxsBytes, int64(written.Mempool.MaxTxBytes),
			"the queue must hold at least one maximum-size transaction")
	})

	t.Run("the count cap and the per-transaction cap are left alone", func(t *testing.T) {
		// Recorded as a decision, not an oversight. 5000 transactions of the 310
		// bytes a signed single MsgSend occupies is 1550000 bytes, under a tenth
		// of one block's data budget, so the count already binds far tighter than
		// any byte cap for ordinary traffic; lowering it would only shrink the
		// honest burst a node can hold. If either default moves, that reasoning
		// has to be redone rather than assumed.
		require.Equal(t, 5000, defaults.Mempool.Size)
		require.Equal(t, defaults.Mempool.Size, written.Mempool.Size)
		require.Equal(t, 1048576, defaults.Mempool.MaxTxBytes)
		require.Equal(t, defaults.Mempool.MaxTxBytes, written.Mempool.MaxTxBytes)
	})

	t.Run("blocks are paced at the interval the reward schedule assumes", func(t *testing.T) {
		// An ECONOMIC assertion wearing operational clothes. Emission is a
		// per-block subsidy and nothing in the state machine reads a clock, so the
		// wall-clock emission rate is decided by block pacing alone: 5-second
		// blocks give ~12.5% of supply in year one and a first halving near four
		// years, 1-second blocks ~62% and under ten months.
		//
		// THIS ASSERTS THE OUTCOME, NOT THE CALL SITE, and the distinction is worth
		// stating because it is easy to misread as a stronger proof than it is.
		// Deleting nodeConfig's assignment does NOT turn this red today: the SDK
		// overrides TimeoutCommit to 5s whenever the config it receives still holds
		// CometBFT's 1s default (server/util.go), so both paths produce the same
		// file. Verified by doing exactly that and diffing the written config.toml.
		//
		// It is worth having anyway, because what must not silently change is the
		// VALUE an operator's node ends up pacing at. This fails if the SDK revises
		// its opinion, if CometBFT moves its default, or if the reward schedule's
		// assumed block time moves away from the node's — whichever supplies it.
		require.Equal(t, 1000*time.Millisecond, defaults.Consensus.TimeoutCommit,
			"the upstream default this exists to replace has moved; re-derive before touching this")

		want := time.Duration(rewardstypes.DefaultTargetBlockTimeSeconds) * time.Second
		// Deliberately NOT phrased as "the customization never reached the SDK".
		// That cause cannot be established here — the SDK supplies the same value
		// when nodeConfig sets nothing — and claiming it would send a reader
		// chasing the wrong thing the day CometBFT moves its own default.
		require.NotEqual(t, defaults.Consensus.TimeoutCommit, written.Consensus.TimeoutCommit,
			"the written pacing equals CometBFT's default, so nothing is pacing this chain deliberately")
		require.Equal(t, want, written.Consensus.TimeoutCommit,
			"the pacing a node writes must match the block time the reward schedule is written against")
	})

	t.Run("bounding the queue did not introduce a fee", func(t *testing.T) {
		// The canonical reward pool has no fee term and fee_collector is wired
		// with no permissions, so a non-zero minimum would collect into an account
		// with no defined destination. Zero is deliberate and stays.
		appToml, err := os.ReadFile(filepath.Join(home, "config", "app.toml"))
		require.NoError(t, err)
		require.Contains(t, string(appToml), `minimum-gas-prices = "0utwlt"`)
	})
}

// readCometConfig parses a config.toml off disk onto the CometBFT defaults, the
// way a starting node does, so a key the file does NOT set reads back as the
// upstream default rather than as a zero value.
func readCometConfig(t *testing.T, path string) *cmtcfg.Config {
	t.Helper()
	v := viper.New()
	v.SetConfigFile(path)
	require.NoError(t, v.ReadInConfig())
	cfg := cmtcfg.DefaultConfig()
	require.NoError(t, v.Unmarshal(cfg))
	return cfg
}
