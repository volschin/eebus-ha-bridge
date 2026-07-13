#!/bin/bash
# Generate Python gRPC stubs from protobuf definitions
set -euo pipefail

# Home Assistant Core pins grpcio==1.78.0 in package_constraints.txt.
# Stubs must be generated with a matching grpcio-tools so the embedded
# GRPC_GENERATED_VERSION does not exceed HA's runtime grpcio (see issue #22).
GRPCIO_TOOLS_VERSION="${GRPCIO_TOOLS_VERSION:-1.78.0}"

PROTO_DIR="eebus-bridge/proto"
OUT_DIR="custom_components/eebus/generated"

mkdir -p "$OUT_DIR"

pip install --quiet "grpcio-tools==${GRPCIO_TOOLS_VERSION}"

python -m grpc_tools.protoc \
  -I "$PROTO_DIR" \
  --python_out="$OUT_DIR" \
  --grpc_python_out="$OUT_DIR" \
  --pyi_out="$OUT_DIR" \
  eebus/v1/common.proto \
  eebus/v1/device_service.proto \
  eebus/v1/lpc_service.proto \
  eebus/v1/monitoring_service.proto \
  eebus/v1/grid_service.proto \
  eebus/v1/visualization_service.proto \
  eebus/v1/ohpcf_service.proto \
  eebus/v1/dhw_service.proto \
  eebus/v1/hvac_service.proto

touch "$OUT_DIR/__init__.py"
touch "$OUT_DIR/eebus/__init__.py"
touch "$OUT_DIR/eebus/v1/__init__.py"

# Rewrite absolute "from eebus.v1 import" to relative imports so the stubs
# work when loaded as part of custom_components.eebus (not as a top-level package).
find "$OUT_DIR/eebus/v1" \( -name "*.py" -o -name "*.pyi" \) -exec sed -i \
  's/^from eebus\.v1 import \(.*\) as \(.*\)$/from . import \1 as \2/' {} \;

echo "Proto stubs generated in $OUT_DIR"
