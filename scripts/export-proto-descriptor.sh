#!/usr/bin/env bash
# Export a self-contained protobuf FileDescriptorSet for the Twilight custom modules
# (coreslot + rewards) plus all transitive Cosmos/gogo/google dependencies, so that
# downstream clients/indexers (e.g. twilight-core-explorer) can decode raw txs and the
# custom Msg Any-payloads OFFLINE — without importing the chain's Go types.
#
# Tooling only: does NOT touch chain runtime, keepers, Msg handlers, genesis, stores,
# validator updates, or codegen. It only reads proto/ and writes under docs/proto/.
#
# Outputs:
#   docs/proto/twilight-descriptors.pb       binary FileDescriptorSet (--include_imports)
#   docs/proto/twilight-msg-type-urls.json   manifest of registered SDK Msg type URLs
#
# Include paths mirror scripts/protocgen.sh — keep the pinned versions in sync with it.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

# Expected protoc version. The descriptor set is a binary FileDescriptorSet whose bytes
# are reproducible for a fixed protoc version + inputs, so CI re-generates and diffs it
# against the committed copy (.github/workflows/ci.yml: proto-descriptor job). Regenerate
# locally with this same version, or CI's stale check may flag byte-level drift.
EXPECTED_PROTOC_VERSION="27.0"

# --- preflight: required tools -------------------------------------------------------
command -v protoc >/dev/null 2>&1 || {
  echo "ERROR: protoc not found on PATH. Install the protobuf compiler (e.g. 'brew install protobuf' or your distro package)." >&2
  exit 1
}
PROTOC_VERSION="$(protoc --version | awk '{print $2}')"
if [[ "$PROTOC_VERSION" != "$EXPECTED_PROTOC_VERSION" ]]; then
  echo "WARNING: protoc $PROTOC_VERSION != expected $EXPECTED_PROTOC_VERSION." >&2
  echo "         The committed docs/proto/twilight-descriptors.pb was built with" >&2
  echo "         $EXPECTED_PROTOC_VERSION; a different version may produce byte-level drift" >&2
  echo "         that the CI stale check will flag. Install protoc $EXPECTED_PROTOC_VERSION to match." >&2
fi
command -v go >/dev/null 2>&1 || {
  echo "ERROR: go not found on PATH (needed to resolve the module-cache include paths)." >&2
  exit 1
}

# --- include paths (mirror scripts/protocgen.sh) -------------------------------------
GP="$(go env GOPATH)/pkg/mod"
SDK="$GP/github.com/cosmos/cosmos-sdk@v0.53.7"
GOGO="$GP/github.com/cosmos/gogoproto@v1.7.2"
API="$GP/cosmossdk.io/api@v0.9.2"
COSMOS_PROTO="$GP/github.com/cosmos/cosmos-proto@v1.0.0-beta.5"
GOOGLEAPIS="$GP/github.com/gogo/googleapis@v1.4.1"

# --- preflight: required include directories -----------------------------------------
for d in "proto" "$SDK/proto" "$GOGO" "$GOGO/protobuf" "$API" "$COSMOS_PROTO/proto" "$GOOGLEAPIS"; do
  [[ -d "$d" ]] || {
    echo "ERROR: include path missing: $d" >&2
    echo "Run 'go mod download' to populate the module cache, or update the pinned" >&2
    echo "versions in this script to match scripts/protocgen.sh." >&2
    exit 1
  }
done

INCLUDES=(
  -I proto
  -I "$SDK/proto"
  -I "$GOGO"
  -I "$GOGO/protobuf"
  -I "$API"
  -I "$COSMOS_PROTO/proto"
  -I "$GOOGLEAPIS"
)

# --- root proto inputs ---------------------------------------------------------------
# Twilight module protos are referenced relative to `-I proto`; the Cosmos SDK tx/auth/
# base roots are referenced relative to `-I $SDK/proto`. The SDK tx envelope
# (cosmos.tx.v1beta1.Tx/TxRaw/TxBody/AuthInfo + signing) is NOT imported by the Twilight
# protos, so it must be listed explicitly here — a raw CometBFT tx is decoded as TxRaw ->
# TxBody.messages[] (Anys) BEFORE the twilight Msg type_urls can be resolved.
TWILIGHT_PROTOS=(
  proto/twilight/coreslot/v1/coreslot.proto
  proto/twilight/coreslot/v1/genesis.proto
  proto/twilight/coreslot/v1/query.proto
  proto/twilight/coreslot/v1/tx.proto
  proto/twilight/rewards/v1/genesis.proto
  proto/twilight/rewards/v1/params.proto
  proto/twilight/rewards/v1/query.proto
  proto/twilight/rewards/v1/rewards.proto
  proto/twilight/rewards/v1/tx.proto
  proto/twilight/mining/v1/mining.proto
  proto/twilight/mining/v1/genesis.proto
)
# Cosmos SDK tx/auth/base roots for raw transaction decoding (resolved via -I $SDK/proto).
COSMOS_PROTOS=(
  cosmos/tx/v1beta1/tx.proto
  cosmos/tx/signing/v1beta1/signing.proto
  cosmos/base/v1beta1/coin.proto
  cosmos/base/query/v1beta1/pagination.proto
  cosmos/auth/v1beta1/auth.proto
  cosmos/auth/v1beta1/query.proto
  cosmos/bank/v1beta1/bank.proto
  cosmos/bank/v1beta1/query.proto
  # Bank Msg roots (MsgSend / MsgMultiSend) — TxBody.messages[] Any payloads for
  # `tx bank send` / `multi-send`, decodable offline by downstream indexers.
  cosmos/bank/v1beta1/tx.proto
  # Signer public-key Any types in AuthInfo.signer_infos[].public_key, so a fully
  # decoded tx envelope can resolve signer pubkeys (not just TxBody.messages[]).
  cosmos/crypto/secp256k1/keys.proto
  cosmos/crypto/ed25519/keys.proto
  cosmos/crypto/multisig/keys.proto
)
# Validate Twilight protos against the repo, and Cosmos roots against the SDK include.
for p in "${TWILIGHT_PROTOS[@]}"; do
  [[ -f "$p" ]] || { echo "ERROR: proto file missing: $p" >&2; exit 1; }
done
for p in "${COSMOS_PROTOS[@]}"; do
  [[ -f "$SDK/proto/$p" ]] || { echo "ERROR: Cosmos SDK proto missing: $SDK/proto/$p (run 'go mod download')" >&2; exit 1; }
done
PROTOS=("${TWILIGHT_PROTOS[@]}" "${COSMOS_PROTOS[@]}")

OUT_DIR="docs/proto"
mkdir -p "$OUT_DIR"
DESC="$OUT_DIR/twilight-descriptors.pb"
MANIFEST="$OUT_DIR/twilight-msg-type-urls.json"

# --- 1) the descriptor set (self-contained: --include_imports bundles all deps) ------
echo "Generating $DESC (FileDescriptorSet, --include_imports) ..."
protoc "${INCLUDES[@]}" --include_imports --descriptor_set_out="$DESC" "${PROTOS[@]}"
echo "  wrote $DESC ($(wc -c < "$DESC" | tr -d ' ') bytes)"

# --- 2) the Msg type-URL manifest ----------------------------------------------------
# coreslot/rewards entries are the concrete sdk.Msg implementations registered in
# x/coreslot/types/codec.go and x/rewards/types/codec.go; the bank entries are the
# SDK-native MsgSend/MsgMultiSend now reachable via `tx bank send`. Decode each tx
# message's Any.value against the matching message in twilight-descriptors.pb.
cat > "$MANIFEST" <<'JSON'
{
  "note": "Raw-tx decode: a CometBFT tx is cosmos.tx.v1beta1.TxRaw -> decode TxBody (TxRaw.body_bytes) -> for each TxBody.messages[] Any, match Any.type_url below and decode Any.value against the same fully-qualified message in twilight-descriptors.pb. All listed types resolve from that single descriptor set. Generated by scripts/export-proto-descriptor.sh.",
  "descriptor_set": "twilight-descriptors.pb",
  "envelope": {
    "tx_raw": "cosmos.tx.v1beta1.TxRaw",
    "tx": "cosmos.tx.v1beta1.Tx",
    "tx_body": "cosmos.tx.v1beta1.TxBody",
    "auth_info": "cosmos.tx.v1beta1.AuthInfo",
    "signer_info": "cosmos.tx.v1beta1.SignerInfo",
    "sign_mode": "cosmos.tx.signing.v1beta1.SignMode",
    "signer_pubkey_any_types": [
      "/cosmos.crypto.secp256k1.PubKey",
      "/cosmos.crypto.ed25519.PubKey",
      "/cosmos.crypto.multisig.LegacyAminoPubKey"
    ]
  },
  "modules": {
    "bank": [
      "/cosmos.bank.v1beta1.MsgSend",
      "/cosmos.bank.v1beta1.MsgMultiSend"
    ],
    "coreslot": [
      "/twilight.coreslot.v1.MsgRegisterCoreSlot",
      "/twilight.coreslot.v1.MsgActivateCoreSlot",
      "/twilight.coreslot.v1.MsgInactivateCoreSlot",
      "/twilight.coreslot.v1.MsgSuspendCoreSlot",
      "/twilight.coreslot.v1.MsgRemoveCoreSlot",
      "/twilight.coreslot.v1.MsgRotateConsensusKey",
      "/twilight.coreslot.v1.MsgUpdatePayoutAddress",
      "/twilight.coreslot.v1.MsgUpdateOperatorMetadata",
      "/twilight.coreslot.v1.MsgUpdateSettlementAddress",
      "/twilight.coreslot.v1.MsgUpdateSelectionPolicy",
      "/twilight.coreslot.v1.MsgUpdateParams"
    ],
    "rewards": [
      "/twilight.rewards.v1.MsgClaimRewards",
      "/twilight.rewards.v1.MsgUpdateRewardsParams",
      "/twilight.rewards.v1.MsgPauseRewards",
      "/twilight.rewards.v1.MsgResumeRewards"
    ]
  }
}
JSON
echo "  wrote $MANIFEST"

echo "Done."
echo "Sanity-check the packages are present in the descriptor:"
echo "  protoc --decode_raw < $DESC >/dev/null && echo 'descriptor parses'"
