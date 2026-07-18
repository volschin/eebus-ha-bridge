"""Immutable public diagnostics projections for one EEBUS device session."""

from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime


@dataclass(frozen=True, slots=True)
class StreamWorkerDiagnostics:
    configured: int
    running: int
    done: int
    reconnects: int


@dataclass(frozen=True, slots=True)
class DeviceStreamDiagnostics:
    last_device_state_revision: int | None
    refresh_pending: bool
    refresh_running: bool
    primary: StreamWorkerDiagnostics
    legacy: StreamWorkerDiagnostics


@dataclass(frozen=True, slots=True)
class RecoveryProjection:
    state: str
    attempts: int
    first_stale_at: datetime | None
    last_attempt_at: datetime | None
    next_attempt_at: datetime | None
    last_transition_at: datetime | None


@dataclass(frozen=True, slots=True)
class ProviderSampleProjection:
    provider: str
    state: str
    observed_at: datetime | None
    valid_until: datetime | None


@dataclass(frozen=True, slots=True)
class OperationalDiagnostics:
    redacted_ski: str
    readiness: str
    recovery: RecoveryProjection
    event_revision: int
    dropped_events: int
    resync_count: int
    unresolved_events: int
    connection_age_seconds: int | None
    monitoring_last_success_age_seconds: int | None
    snapshot_duration_milliseconds: int
    snapshot_last_success: datetime | None
    providers: tuple[ProviderSampleProjection, ...]
    features: tuple[str, ...]


@dataclass(frozen=True, slots=True)
class SessionDiagnostics:
    bridge_unavailable: bool
    last_successful_read_age_seconds: float | None
    last_update_success: bool
    streams: DeviceStreamDiagnostics
    operational: OperationalDiagnostics | None
