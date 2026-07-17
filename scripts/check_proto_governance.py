"""Check local proto compatibility rules not covered by buf breaking."""

from __future__ import annotations

import re
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
PROTO_ROOT = ROOT / "eebus-bridge" / "proto"

REQUIRED_V1_RPCS = {
    "LPCService": {
        "StartHeartbeat": "deprecated",
        "StopHeartbeat": "deprecated",
        "SubscribeLPCEvents": "stream",
    },
    "DeviceService": {
        "GetDeviceCapabilities": "unary",
        "SubscribeDeviceState": "stream",
        "SubscribeDeviceEvents": "stream",
    },
    "MonitoringService": {"SubscribeMeasurements": "stream"},
    "OHPCFService": {"SubscribeOHPCFEvents": "stream"},
    "DHWService": {
        "SubscribeDHWEvents": "stream",
        "SubscribeDHWSystemFunctionEvents": "stream",
    },
    "HVACService": {"SubscribeRoomHeatingEvents": "stream"},
}


def main() -> None:
    contents = "\n".join(path.read_text() for path in sorted(PROTO_ROOT.rglob("*.proto")))
    packages = set(re.findall(r"(?m)^package\s+([^;]+);$", contents))
    if packages != {"eebus.v1"}:
        raise SystemExit(f"all additive proto files must stay in eebus.v1, got {sorted(packages)}")
    for service, rpcs in REQUIRED_V1_RPCS.items():
        block = _service_block(contents, service)
        for rpc, mode in rpcs.items():
            rpc_line = _rpc_line(block, rpc)
            if mode == "stream" and "stream " not in rpc_line:
                raise SystemExit(f"{service}.{rpc} must remain a v1 stream RPC")
            if mode == "deprecated" and "deprecated = true" not in block[block.find(rpc_line): block.find(rpc_line) + 220]:
                raise SystemExit(f"{service}.{rpc} may only be removed in a new API package; keep it deprecated in v1")


def _service_block(contents: str, service: str) -> str:
    match = re.search(rf"service\s+{re.escape(service)}\s*\{{(?P<body>.*?)\n\}}", contents, re.DOTALL)
    if not match:
        raise SystemExit(f"missing required v1 service {service}")
    return match.group("body")


def _rpc_line(block: str, rpc: str) -> str:
    match = re.search(rf"rpc\s+{re.escape(rpc)}\s*\([^)]*\)\s+returns\s+\([^)]*\)", block)
    if not match:
        raise SystemExit(f"missing required v1 RPC {rpc}")
    return match.group(0)


if __name__ == "__main__":
    main()
