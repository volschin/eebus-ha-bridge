#!/usr/bin/env bash
# Regenerate Go and Python gRPC stubs from protobuf definitions.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${ROOT_DIR}/proto-tools.env"

PYTHON_BIN="${PYTHON_BIN:-python3}"
PROTO_DIR="${ROOT_DIR}/eebus-bridge/proto"
PY_OUT_DIR="${ROOT_DIR}/custom_components/eebus/generated"
PY_PKG_DIR="${PY_OUT_DIR}/eebus/v1"
GO_OUT_DIR="${ROOT_DIR}/eebus-bridge/gen/proto/eebus/v1"
TOOL_BIN="${ROOT_DIR}/.cache/proto-tools/bin"

mkdir -p "${TOOL_BIN}" "${PY_PKG_DIR}" "${GO_OUT_DIR}"

echo "Installing pinned proto tools into ${TOOL_BIN}"
GOBIN="${TOOL_BIN}" go install "github.com/bufbuild/buf/cmd/buf@v${BUF_VERSION}"
GOBIN="${TOOL_BIN}" go install "google.golang.org/protobuf/cmd/protoc-gen-go@v${PROTOC_GEN_GO_VERSION}"
GOBIN="${TOOL_BIN}" go install "google.golang.org/grpc/cmd/protoc-gen-go-grpc@v${PROTOC_GEN_GO_GRPC_VERSION}"

export PATH="${TOOL_BIN}:${PATH}"

echo "Installing pinned grpcio-tools ${GRPCIO_TOOLS_VERSION}"
"${PYTHON_BIN}" -m pip install --quiet "grpcio-tools==${GRPCIO_TOOLS_VERSION}"

echo "Regenerating Go protobuf stubs"
find "${GO_OUT_DIR}" -maxdepth 1 -type f -name "*.pb.go" -delete
"${TOOL_BIN}/buf" generate --template "${ROOT_DIR}/buf.gen.yaml"

echo "Regenerating Python protobuf stubs"
find "${PY_PKG_DIR}" -maxdepth 1 -type f \( -name "*.py" -o -name "*.pyi" \) -delete

mapfile -t PROTO_FILES < <(
  cd "${PROTO_DIR}"
  find eebus/v1 -maxdepth 1 -type f -name "*.proto" | sort
)

"${PYTHON_BIN}" -m grpc_tools.protoc \
  -I "${PROTO_DIR}" \
  --python_out="${PY_OUT_DIR}" \
  --grpc_python_out="${PY_OUT_DIR}" \
  --pyi_out="${PY_OUT_DIR}" \
  "${PROTO_FILES[@]}"

touch "${PY_OUT_DIR}/__init__.py"
touch "${PY_OUT_DIR}/eebus/__init__.py"
touch "${PY_PKG_DIR}/__init__.py"

"${PYTHON_BIN}" -m scripts.proto_postprocess "${PY_PKG_DIR}"

echo "Proto stubs generated in ${GO_OUT_DIR} and ${PY_OUT_DIR}"
