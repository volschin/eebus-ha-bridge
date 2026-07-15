"""Tests for secure gRPC channel construction."""

from unittest.mock import MagicMock, patch

import pytest

from custom_components.eebus.const import (
    SECURITY_MODE_LOOPBACK,
    SECURITY_MODE_TLS_TOKEN,
)
from custom_components.eebus.security import create_grpc_channel


def test_loopback_channel_keeps_existing_plaintext_default():
    """Loopback mode creates the existing plaintext channel."""
    channel = MagicMock()
    with patch("custom_components.eebus.security.grpc.aio.insecure_channel", return_value=channel) as create:
        assert create_grpc_channel("127.0.0.1", 50051) is channel
    create.assert_called_once_with("127.0.0.1:50051")


def test_loopback_channel_rejects_remote_host():
    """Plaintext cannot carry bridge traffic to a remote host."""
    with pytest.raises(ValueError, match="loopback"):
        create_grpc_channel("192.0.2.10", 50051, SECURITY_MODE_LOOPBACK)


def test_tls_token_channel_uses_composite_credentials():
    """TLS mode combines CA trust and per-RPC token credentials."""
    transport = MagicMock()
    per_rpc = MagicMock()
    combined = MagicMock()
    channel = MagicMock()
    with (
        patch("custom_components.eebus.security.grpc.ssl_channel_credentials", return_value=transport) as ssl,
        patch("custom_components.eebus.security.grpc.access_token_call_credentials", return_value=per_rpc) as token,
        patch("custom_components.eebus.security.grpc.composite_channel_credentials", return_value=combined) as composite,
        patch("custom_components.eebus.security.grpc.aio.secure_channel", return_value=channel) as secure,
    ):
        result = create_grpc_channel(
            "bridge.example.test",
            50051,
            SECURITY_MODE_TLS_TOKEN,
            "test-ca-pem",
            "test-auth-token",
        )

    assert result is channel
    ssl.assert_called_once_with(root_certificates=b"test-ca-pem")
    token.assert_called_once_with("test-auth-token")
    composite.assert_called_once_with(transport, per_rpc)
    secure.assert_called_once_with("bridge.example.test:50051", combined)
