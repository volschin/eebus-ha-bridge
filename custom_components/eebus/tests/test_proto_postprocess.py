"""Tests for deterministic protobuf postprocessing."""

from pathlib import Path

from scripts.proto_postprocess import rewrite_python_proto_imports


def test_rewrite_python_proto_imports_is_deterministic(tmp_path: Path) -> None:
    """Absolute generated imports are rewritten once and then remain stable."""
    generated = tmp_path / "eebus" / "v1"
    generated.mkdir(parents=True)
    target = generated / "device_service_pb2.py"
    untouched = generated / "common_pb2_grpc.py"
    target.write_text(
        "from eebus.v1 import common_pb2 as eebus_dot_v1_dot_common__pb2\n"
        "value = 1\n"
    )
    untouched.write_text("import grpc\n")

    assert rewrite_python_proto_imports(generated) == [target]
    first = target.read_text()
    assert first == "from . import common_pb2 as eebus_dot_v1_dot_common__pb2\nvalue = 1\n"
    assert rewrite_python_proto_imports(generated) == []
    assert target.read_text() == first
