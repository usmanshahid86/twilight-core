# Twilight Devnet — Operator Join Guide

A public, two-validator **devnet** for the Twilight CoreSlot Proof-of-Authority
chain with the `utwlt` rewards module. Anyone can run a full node, sync the chain,
query state, and submit transactions; operators can be onboarded as CoreSlot
validators by the chain authority. This is a **throwaway test network** — it may be
wiped and re-genesis'd at any time (you'll just re-join).

> Both genesis validators must be online for the chain to produce blocks. CometBFT
> needs more than 2/3 of voting power, and with two equal validators that means both.
> A halt while one is restarting is expected behaviour, not a fault.

## Network details

| | |
|---|---|
| chain-id | `twilight-devnet-2` |
| RPC | `http://13.215.26.158:26657` |
| seed / peer | `ea79c42d88c04ffda9c695c4af1218fdcb6b0c75@13.215.26.158:26656` |
| genesis | [`devnet/genesis.json`](./genesis.json) in this repo (or fetch from RPC, below) |
| gas | gasless — `--minimum-gas-prices 0utwlt` |
| denom | `utwlt` (accounting); `twlt`/`TWLT` is display-only |

> The RPC at `:26657` is open for devnet testing and may move behind a controlled
> node later. The p2p port `:26656` is the stable join point.

## Prerequisites

- Linux host, ~2 GB free disk.
- **Go 1.25.x** to build (Go 1.26 breaks a pinned dependency). Get the latest
  1.25.x from <https://go.dev/dl/>.
- `git`, `jq`, `curl`.

## 1. Build `twilightd`

```bash
git clone https://github.com/usmanshahid86/twilight-core
cd twilight-core
CGO_ENABLED=0 go build -o build/twilightd ./cmd/twilightd
sudo install build/twilightd /usr/local/bin/twilightd
```

## 2. Initialize and install genesis

```bash
twilightd init <your-moniker> --chain-id twilight-devnet-2

# genesis: use the version-controlled copy (exact)…
cp devnet/genesis.json ~/.twilightd/config/genesis.json
# …or fetch it live:
# curl -s http://13.215.26.158:26657/genesis | jq '.result.genesis' > ~/.twilightd/config/genesis.json

jq -r '.chain_id' ~/.twilightd/config/genesis.json   # -> twilight-devnet-2
```

## 3. Point at the seed

```bash
sed -i 's#^persistent_peers =.*#persistent_peers = "ea79c42d88c04ffda9c695c4af1218fdcb6b0c75@13.215.26.158:26656"#' \
  ~/.twilightd/config/config.toml
```

## 4. Run as a systemd service (recommended)

Running under systemd means the node **auto-restarts on crash and survives
reboots** — do this from the start so your node is robust.

```bash
sudo tee /etc/systemd/system/twilightd.service >/dev/null <<UNIT
[Unit]
Description=Twilight devnet full node
After=network-online.target
Wants=network-online.target

[Service]
User=$USER
ExecStart=/usr/local/bin/twilightd start --minimum-gas-prices 0utwlt
Restart=always
RestartSec=3
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
UNIT

sudo systemctl daemon-reload
sudo systemctl enable --now twilightd
journalctl -u twilightd -f          # watch it sync (Ctrl-C to stop watching)
```

> Quick foreground alternative (testing only, dies on logout):
> `twilightd start --minimum-gas-prices 0utwlt`

## 5. Verify you're synced

```bash
curl -s http://localhost:26657/status | jq '.result.sync_info | {height: .latest_block_height, catching_up}'
curl -s http://13.215.26.158:26657/status | jq -r '.result.sync_info.latest_block_height'   # seed head
```

`catching_up: false` with a height tracking the seed = fully joined.

## 6. (Optional) Become a CoreSlot validator

CoreSlot is Proof-of-Authority: validators are **registered by the chain
authority**, not by self-stake. Onboarding is a two-side process.

**On your node — gather your details:**

```bash
twilightd keys add operator --keyring-backend test          # your operator account
OPERATOR=$(twilightd keys show operator -a --keyring-backend test)
PUBKEY=$(twilightd comet show-validator | jq -r .key)        # your consensus pubkey (base64)
echo "operator=$OPERATOR"; echo "pubkey=$PUBKEY"; echo "moniker=<your-moniker>"
```

Send the devnet authority your **operator address**, a **payout address** (can be
the same), a **settlement address** (also can be the same — it is the operational
credential you sign settlement-side messages with, and it is mandatory), your
**consensus pubkey (base64)**, and a **moniker**.

**On the devnet authority (the seed operator runs this):**

```bash
H=~/.twilight-devnet; COMMON="--from validator --keyring-backend test --home $H \
  --chain-id twilight-devnet-2 --node http://localhost:26657 --gas 400000 --fees 0utwlt -y"

twilightd coreslot register <operator> <payout> <settlement> <consensus-pubkey-base64> "<moniker>" $COMMON
twilightd coreslot-query slots --node http://localhost:26657 -o json | jq   # find the new slot id
twilightd coreslot activate <slot-id> $COMMON                                # if not already active
twilightd coreslot-query active --node http://localhost:26657 -o json | jq
```

Registration also records the slot's initial Selection policy. The example relies
on the command's defaults (`--selection-rate-bps 2500`,
`--max-selected-participants 10`); both are operator configuration and carry no
protocol significance, so pass the flags explicitly if the slot needs different
values.

Once your consensus key is active, your **already-running node starts validating
automatically** — CometBFT applies the validator-set change live, no restart
needed. Confirm with `curl -s http://localhost:26657/status | jq '.result.validator_info.voting_power'` (should become `1`).

## Interacting with the chain

```bash
twilightd rewards-query epoch-info         --node http://13.215.26.158:26657 -o json
twilightd rewards-query cumulative-emitted --node http://13.215.26.158:26657 -o json
twilightd rewards-query module-balances    --node http://13.215.26.158:26657 -o json
twilightd coreslot-query active            --node http://13.215.26.158:26657 -o json
```

Rewards finalize ~every 30 minutes (`epoch_length_blocks = 360` at ~5 s blocks). 360 is
the immutable floor — the admissible interval is `[360, 720]` (`app/params/bounds.go`),
so a shorter devnet epoch is no longer possible. Claims are anyone-triggered and pay the
snapshotted payout to the slot's payout address — see the rewards docs in
[`website/`](../website).

## Notes

- **Devnet only — no persistence guarantees.** On a reset, run
  `twilightd comet unsafe-reset-all --home ~/.twilightd` (this wipes the databases and
  zeroes `data/priv_validator_state.json` while keeping your keys), remove the old
  `config/genesis.json`, install the new one, and restart the service.
- **`twilightd init` does not regenerate `app.toml`.** An existing one is left untouched,
  so pruning and API settings carried over from a previous deployment survive a re-genesis
  silently. Check `pruning` and `min-retain-blocks` after re-initialising.
- `scripts/devnet/devnet-up.sh` is **stale** and cannot build a valid genesis for this
  network: it forces `EPOCH_LENGTH=60`, below the immutable floor of 360. It needs a fix
  before it is used again. This network was assembled with `coreslot-genesis
  set-authorities` / `add` / `validate` directly.
