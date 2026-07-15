"""Secure gRPC channel construction for the EEBUS integration."""

from __future__ import annotations

import ipaddress

import grpc
import grpc.aio

from .const import SECURITY_MODE_LOOPBACK, SECURITY_MODE_TLS_TOKEN


def _target(host: str, port: int) -> str:
    """Format an IPv4, IPv6, or hostname gRPC target."""
    if ":" in host and not host.startswith("["):
        return f"[{host}]:{port}"
    return f"{host}:{port}"


def _is_loopback_host(host: str) -> bool:
    """Return whether a configured bridge host is local-only."""
    if host.casefold() == "localhost":
        return True
    try:
        return ipaddress.ip_address(host).is_loopback
    except ValueError:
        return False


def create_grpc_channel(
    host: str,
    port: int,
    security_mode: str = SECURITY_MODE_LOOPBACK,
    tls_ca_certificate: str | None = None,
    auth_token: str | None = None,
) -> grpc.aio.Channel:
    """Create a plaintext loopback or authenticated TLS gRPC channel."""
    target = _target(host, port)
    if security_mode == SECURITY_MODE_LOOPBACK:
        if not _is_loopback_host(host):
            raise ValueError("loopback security mode requires a loopback bridge host")
        return grpc.aio.insecure_channel(target)
    if security_mode != SECURITY_MODE_TLS_TOKEN:
        raise ValueError(f"unsupported gRPC security mode: {security_mode}")
    if not tls_ca_certificate or not auth_token:
        raise ValueError("tls_token mode requires a CA certificate and auth token")

    transport = grpc.ssl_channel_credentials(
        root_certificates=tls_ca_certificate.encode()
    )
    token_credentials = grpc.access_token_call_credentials(auth_token)
    credentials = grpc.composite_channel_credentials(transport, token_credentials)
    return grpc.aio.secure_channel(target, credentials)
