#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SDK="$(go env GOPATH)/pkg/mod/github.com/cosmos/cosmos-sdk@v0.53.7"
GOGO="$(go env GOPATH)/pkg/mod/github.com/cosmos/gogoproto@v1.7.2"
API="$(go env GOPATH)/pkg/mod/cosmossdk.io/api@v0.9.2"
COSMOS_PROTO="$(go env GOPATH)/pkg/mod/github.com/cosmos/cosmos-proto@v1.0.0-beta.5"
GOOGLEAPIS="$(go env GOPATH)/pkg/mod/github.com/gogo/googleapis@v1.4.1"

cd "$ROOT"
protoc \
  -I proto \
  -I "$SDK/proto" \
  -I "$GOGO" \
  -I "$GOGO/protobuf" \
  -I "$API" \
  -I "$COSMOS_PROTO/proto" \
  -I "$GOOGLEAPIS" \
  --gocosmos_out=plugins=grpc,paths=source_relative,Mgoogle/protobuf/any.proto=github.com/cosmos/gogoproto/types/any:. \
  proto/twilight/coreslot/v1/coreslot.proto \
  proto/twilight/coreslot/v1/genesis.proto \
  proto/twilight/coreslot/v1/tx.proto \
  proto/twilight/coreslot/v1/query.proto

mkdir -p x/coreslot/types
mv twilight/coreslot/v1/*.go x/coreslot/types/
rm -rf twilight
sed -i.bak 's#github.com/gogo/protobuf/grpc#github.com/cosmos/gogoproto/grpc#g' x/coreslot/types/*.pb.go
sed -i.bak 's#github.com/gogo/protobuf/proto#github.com/cosmos/gogoproto/proto#g' x/coreslot/types/*.pb.go
sed -i.bak 's#_ "google/api"#_ "google.golang.org/genproto/googleapis/api/annotations"#g' x/coreslot/types/*.pb.go
rm -f x/coreslot/types/*.pb.go.bak
gofmt -w x/coreslot/types/*.pb.go
