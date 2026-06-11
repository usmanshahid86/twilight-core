#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SDK="$(go env GOPATH)/pkg/mod/github.com/cosmos/cosmos-sdk@v0.53.7"
GOGO="$(go env GOPATH)/pkg/mod/github.com/cosmos/gogoproto@v1.7.2"
API="$(go env GOPATH)/pkg/mod/cosmossdk.io/api@v0.9.2"
COSMOS_PROTO="$(go env GOPATH)/pkg/mod/github.com/cosmos/cosmos-proto@v1.0.0-beta.5"
GOOGLEAPIS="$(go env GOPATH)/pkg/mod/github.com/gogo/googleapis@v1.4.1"

cd "$ROOT"
PROTO_ARGS=(
  -I proto
  -I "$SDK/proto"
  -I "$GOGO"
  -I "$GOGO/protobuf"
  -I "$API"
  -I "$COSMOS_PROTO/proto"
  -I "$GOOGLEAPIS"
  --gocosmos_out=plugins=grpc,paths=source_relative,Mgoogle/protobuf/any.proto=github.com/cosmos/gogoproto/types/any:.
)

protoc "${PROTO_ARGS[@]}" \
  proto/twilight/coreslot/v1/coreslot.proto \
  proto/twilight/coreslot/v1/genesis.proto \
  proto/twilight/coreslot/v1/tx.proto \
  proto/twilight/coreslot/v1/query.proto

protoc "${PROTO_ARGS[@]}" \
  proto/twilight/rewards/v1/params.proto \
  proto/twilight/rewards/v1/rewards.proto \
  proto/twilight/rewards/v1/genesis.proto \
  proto/twilight/rewards/v1/tx.proto \
  proto/twilight/rewards/v1/query.proto

mkdir -p x/coreslot/types x/rewards/types
mv twilight/coreslot/v1/*.go x/coreslot/types/
mv twilight/rewards/v1/*.go x/rewards/types/
rm -rf twilight
for types_dir in x/coreslot/types x/rewards/types; do
  sed -i.bak 's#github.com/gogo/protobuf/grpc#github.com/cosmos/gogoproto/grpc#g' "$types_dir"/*.pb.go
  sed -i.bak 's#github.com/gogo/protobuf/proto#github.com/cosmos/gogoproto/proto#g' "$types_dir"/*.pb.go
  sed -i.bak 's#_ "google/api"#_ "google.golang.org/genproto/googleapis/api/annotations"#g' "$types_dir"/*.pb.go
  rm -f "$types_dir"/*.pb.go.bak
done
gofmt -w x/coreslot/types/*.pb.go x/rewards/types/*.pb.go
