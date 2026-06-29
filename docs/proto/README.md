# Twilight Proto Descriptors (for clients / indexers)

This directory holds a **non-runtime** export of the Twilight protobuf schema for
downstream clients — notably `twilight-core-explorer` — to decode raw transactions and
the custom `Msg` payloads **offline**, without importing the chain's Go types.

These files are tooling artifacts. They are **not** used by the chain at runtime and
have no effect on consensus, modules, genesis, or app behaviour.

## Files

| File | What it is |
|---|---|
| `twilight-descriptors.pb` | A self-contained binary `FileDescriptorSet` (protoc `--include_imports`) covering: the **Cosmos SDK tx envelope** (`cosmos.tx.v1beta1.{TxRaw,Tx,TxBody,AuthInfo,SignerInfo,Fee}`, `cosmos.tx.signing.v1beta1.SignMode`), signer pubkey types (`cosmos.crypto.{secp256k1,ed25519,multisig}`), auth/bank/coin/pagination, the Twilight `coreslot` + `rewards` protos, **and all transitive deps**. One file resolves a raw tx end-to-end. |
| `twilight-msg-type-urls.json` | Manifest of the registered SDK `Msg` type URLs (the concrete `sdk.Msg` implementations the chain accepts). |

## Regenerate

```sh
scripts/export-proto-descriptor.sh
```

Requires `protoc` and `go` on `PATH` (the include paths resolve against the Go module
cache; run `go mod download` if they're missing). The script mirrors the include set in
`scripts/protocgen.sh`.

## How an explorer/indexer consumes it

The descriptor set is language-agnostic and self-contained — the same `Root` resolves
the Cosmos tx envelope **and** the twilight `Msg`s. The raw-tx decode flow:

1. `cosmos.tx.v1beta1.TxRaw` ← the raw CometBFT tx bytes
2. `cosmos.tx.v1beta1.TxBody` ← decode `TxRaw.body_bytes`
3. for each `TxBody.messages[]` (an `Any`): look up `Any.type_url`, decode `Any.value`
   against that message — including `/twilight.coreslot.v1.Msg…` and
   `/twilight.rewards.v1.Msg…`

**TypeScript (protobufjs)** — load the descriptor once, then decode a raw tx:

```ts
import protobuf from "protobufjs";
import descriptor from "protobufjs/ext/descriptor";
import fs from "fs";

// One Root resolves every type in the descriptor set (envelope + cosmos + twilight).
const root = (protobuf.Root as any).fromDescriptor(
  descriptor.FileDescriptorSet.decode(
    fs.readFileSync("docs/proto/twilight-descriptors.pb"),
  ),
);

const lookup = (typeUrl: string) => root.lookupType(typeUrl.replace(/^\//, ""));

// Leaf decode -> plain object. bytes:String (base64) is safe ONLY here, because
// nothing decodes the result further.
const decodeMsg = (typeUrl: string, bytes: Uint8Array) => {
  const T = lookup(typeUrl);
  return T.toObject(T.decode(bytes), { longs: String, bytes: String, defaults: true });
};

export function decodeRawTx(txBytes: Uint8Array) {
  // Decode the envelope to message INSTANCES so the *Bytes fields stay Uint8Array
  // for the next hop. (Root.fromDescriptor exposes fields in camelCase.)
  const txRaw = lookup("cosmos.tx.v1beta1.TxRaw").decode(txBytes);
  const body = lookup("cosmos.tx.v1beta1.TxBody").decode(txRaw.bodyBytes);

  const messages = (body.messages ?? []).map((any: any) => ({
    typeUrl: any.typeUrl,                   // camelCase — NOT any.type_url
    ...decodeMsg(any.typeUrl, any.value),   // any.value is a Uint8Array
  }));

  // AuthInfo (fees, signer pubkeys/modes) if needed:
  const authInfo = txRaw.authInfoBytes?.length
    ? decodeMsg("cosmos.tx.v1beta1.AuthInfo", txRaw.authInfoBytes)
    : null;

  return { messages, authInfo, memo: body.memo };
}
```

Two protobufjs gotchas this example deliberately avoids (verified against protobufjs 8.x):

- **`Root.fromDescriptor` yields camelCase field names** — use `txRaw.bodyBytes`,
  `txRaw.authInfoBytes`, and `any.typeUrl` (not `body_bytes` / `type_url`); the snake_case
  keys are `undefined`.
- **Don't `toObject(..., { bytes: String })` on the envelope.** That base64-encodes
  `bodyBytes` / `authInfoBytes` / `Any.value` to strings, and the next `decode()` then
  throws `illegal buffer`. Decode the envelope to instances (bytes stay `Uint8Array`); only
  convert the leaf `Msg` to a plain object.

Everything is included via `--include_imports`, so no other proto sources are needed at
runtime. `cosmos.base.v1beta1.Coin` (fees/amounts) is present; standard `bank`/`auth`
message and query types resolve from the same root.

**Other languages:** the same `.pb` works with any protobuf runtime that can load a
`FileDescriptorSet` (Go `protoregistry`, Python `descriptor_pb2`, etc.).

## Type URLs (authoritative)

The chain registers these `sdk.Msg` implementations (see
`x/coreslot/types/codec.go`, `x/rewards/types/codec.go`); `twilight-msg-type-urls.json`
is the machine-readable copy:

- `coreslot`: `MsgRegisterCoreSlot`, `MsgActivateCoreSlot`, `MsgInactivateCoreSlot`,
  `MsgSuspendCoreSlot`, `MsgRemoveCoreSlot`, `MsgRotateConsensusKey`,
  `MsgUpdatePayoutAddress`, `MsgUpdateOperatorMetadata`, `MsgUpdateParams`
- `rewards`: `MsgClaimRewards`, `MsgUpdateRewardsParams`, `MsgPauseRewards`,
  `MsgResumeRewards`

## Note

This is descriptor export only. Generating typed TS bindings (buf / Telescope /
ts-proto) is intentionally **not** part of the chain repo at this stage.
