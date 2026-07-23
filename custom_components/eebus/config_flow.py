"""Config flow for EEBUS integration."""

from __future__ import annotations

import asyncio
import logging
from dataclasses import dataclass
from typing import Any

import grpc
import voluptuous as vol
from homeassistant.config_entries import (
    ConfigEntry,
    ConfigFlow,
    ConfigFlowResult,
    OptionsFlow,
)
from homeassistant.core import callback
from homeassistant.helpers.selector import (
    EntitySelector,
    EntitySelectorConfig,
    SelectOptionDict,
    SelectSelector,
    SelectSelectorConfig,
    SelectSelectorMode,
    TextSelector,
    TextSelectorConfig,
    TextSelectorType,
)
from homeassistant.helpers.service_info.zeroconf import ZeroconfServiceInfo

from .const import (
    CONF_AUTH_TOKEN,
    CONF_BATTERY_CHARGED_ENERGY_ENTITY,
    CONF_BATTERY_DISCHARGED_ENERGY_ENTITY,
    CONF_BATTERY_POWER_ENTITY,
    CONF_BATTERY_SOC_ENTITY,
    CONF_DEVICE_SKI,
    CONF_GRID_CONSUMPTION_ENERGY_ENTITY,
    CONF_GRID_FEED_IN_ENERGY_ENTITY,
    CONF_GRID_POWER_ENTITY,
    CONF_GRPC_HOST,
    CONF_GRPC_PORT,
    CONF_PV_PEAK_POWER_ENTITY,
    CONF_PV_POWER_ENTITY,
    CONF_PV_YIELD_ENERGY_ENTITY,
    CONF_SECURITY_MODE,
    CONF_TLS_CA_CERTIFICATE,
    DEFAULT_GRPC_PORT,
    DOMAIN,
    SECURITY_MODE_LOOPBACK,
    SECURITY_MODE_TLS_TOKEN,
)
from .security import create_grpc_channel
from .server_info import IncompatibleAPIMajorError, async_read_bridge_contract
from .ski import is_valid_ski, normalize_ski

_LOGGER = logging.getLogger(__name__)

CONFIG_RPC_TIMEOUT = 8
ERROR_CANNOT_CONNECT = "cannot_connect"
ERROR_TLS_TRUST = "tls_trust"
ERROR_INVALID_AUTH = "invalid_auth"
ERROR_INCOMPATIBLE_GRPC = "incompatible_grpc_endpoint"
ERROR_INCOMPATIBLE_API = "incompatible_api_version"


@dataclass(frozen=True, slots=True)
class BridgeProbeResult:
    """Classified result of probing the bridge's DeviceService."""

    local_ski: str | None = None
    error: str | None = None


def _classify_probe_error(err: grpc.aio.AioRpcError) -> str:
    """Map a failed GetStatus probe to the config-flow error bucket."""
    code = err.code()
    if code == grpc.StatusCode.UNAUTHENTICATED:
        return ERROR_INVALID_AUTH
    if code == grpc.StatusCode.UNIMPLEMENTED:
        return ERROR_INCOMPATIBLE_GRPC
    details = (err.details() or "").casefold()
    if code == grpc.StatusCode.UNAVAILABLE and any(
        marker in details
        for marker in ("certificate", "tls", "ssl", "handshake", "x509")
    ):
        return ERROR_TLS_TRUST
    return ERROR_CANNOT_CONNECT


STEP_USER_DATA_SCHEMA = vol.Schema(
    {
        vol.Required(CONF_GRPC_HOST): str,
        vol.Required(CONF_GRPC_PORT, default=DEFAULT_GRPC_PORT): int,
    }
)

SECURITY_MODE_SELECTOR = SelectSelector(
    SelectSelectorConfig(
        options=[SECURITY_MODE_LOOPBACK, SECURITY_MODE_TLS_TOKEN],
        mode=SelectSelectorMode.DROPDOWN,
        translation_key="security_mode",
    )
)


def _security_schema(
    mode: str = SECURITY_MODE_LOOPBACK,
    tls_ca_certificate: str = "",
) -> vol.Schema:
    """Build the shared initial/reconfigure security form."""
    return vol.Schema(
        {
            vol.Required(CONF_SECURITY_MODE, default=mode): SECURITY_MODE_SELECTOR,
            vol.Optional(
                CONF_TLS_CA_CERTIFICATE, default=tls_ca_certificate
            ): TextSelector(TextSelectorConfig(multiline=True)),
            vol.Optional(CONF_AUTH_TOKEN): TextSelector(
                TextSelectorConfig(type=TextSelectorType.PASSWORD)
            ),
        }
    )


def _reauth_schema(tls_ca_certificate: str) -> vol.Schema:
    """Build the credential replacement form used after auth failure."""
    return vol.Schema(
        {
            vol.Required(
                CONF_TLS_CA_CERTIFICATE, default=tls_ca_certificate
            ): TextSelector(TextSelectorConfig(multiline=True)),
            vol.Required(CONF_AUTH_TOKEN): TextSelector(
                TextSelectorConfig(type=TextSelectorType.PASSWORD)
            ),
        }
    )


class EebusConfigFlow(ConfigFlow, domain=DOMAIN):
    """Handle a config flow for EEBUS."""

    VERSION = 2
    DOMAIN = DOMAIN

    @staticmethod
    @callback
    def async_get_options_flow(config_entry: ConfigEntry) -> EebusOptionsFlow:
        """Return the options flow for mapping grid sensors to the bridge."""
        return EebusOptionsFlow()

    def __init__(self) -> None:
        """Initialize."""
        self._host: str = ""
        self._port: int = DEFAULT_GRPC_PORT
        self._local_ski: str = ""
        self._security_mode: str = SECURITY_MODE_LOOPBACK
        self._tls_ca_certificate: str | None = None
        self._auth_token: str | None = None

    async def _async_port_reachable(self, host: str, port: int) -> bool:
        """Check whether something is listening on host:port.

        Zeroconf-discovered SHIP devices are not necessarily the bridge
        itself (every EEBUS device advertises `_ship._tcp`), and the real
        probe in `_async_probe_bridge` needs a security mode/credentials the
        discovery flow doesn't have yet. A bare TCP connect can't confirm
        it's actually the bridge, but it does reject discoveries where
        nothing is listening on the gRPC port at all (e.g. the heat pump
        itself), instead of always sending the user to a security form that
        can never connect.
        """
        try:
            _, writer = await asyncio.wait_for(
                asyncio.open_connection(host, port), timeout=3
            )
        except (OSError, TimeoutError):
            return False
        writer.close()
        try:
            await writer.wait_closed()
        except OSError:
            pass
        return True

    async def _async_probe_bridge(self, host: str, port: int) -> BridgeProbeResult:
        """Check bridge reachability and classify operator-actionable failures."""
        try:
            channel = create_grpc_channel(
                host,
                port,
                self._security_mode,
                self._tls_ca_certificate,
                self._auth_token,
            )
        except ValueError as err:
            _LOGGER.debug("Invalid EEBUS bridge security settings: %s", err)
            return BridgeProbeResult(error=ERROR_CANNOT_CONNECT)
        try:
            contract = await async_read_bridge_contract(channel, timeout=CONFIG_RPC_TIMEOUT)
            return BridgeProbeResult(local_ski=contract.local_ski)
        except IncompatibleAPIMajorError as err:
            _LOGGER.debug("Incompatible EEBUS bridge API at %s:%s: %s", host, port, err)
            return BridgeProbeResult(error=ERROR_INCOMPATIBLE_API)
        except grpc.aio.AioRpcError as err:
            error = _classify_probe_error(err)
            _LOGGER.debug(
                "EEBUS bridge probe at %s:%s failed with %s",
                host,
                port,
                error,
                exc_info=True,
            )
            return BridgeProbeResult(error=error)
        except Exception:
            _LOGGER.debug(
                "No EEBUS bridge reachable at %s:%s", host, port, exc_info=True
            )
            return BridgeProbeResult(error=ERROR_CANNOT_CONNECT)
        finally:
            await channel.close(None)

    async def _async_list_discovered_skis(self) -> list[SelectOptionDict]:
        """Fetch discovered devices from the bridge for the SKI picker."""
        channel = create_grpc_channel(
            self._host,
            self._port,
            self._security_mode,
            self._tls_ca_certificate,
            self._auth_token,
        )
        try:
            from . import proto_stubs

            stub = proto_stubs.device_service_stub(channel)
            response = await stub.ListDiscoveredDevices(
                proto_stubs.Empty(), timeout=CONFIG_RPC_TIMEOUT
            )
            options: list[SelectOptionDict] = []
            for device in response.devices:
                if not device.ski or device.ski == self._local_ski:
                    continue
                name = " ".join(
                    part for part in (device.brand, device.model) if part
                )
                label = f"{name} ({device.ski})" if name else device.ski
                options.append(SelectOptionDict(value=device.ski, label=label))
            return options
        except Exception:
            _LOGGER.debug("Listing discovered devices failed", exc_info=True)
            return []
        finally:
            await channel.close(None)

    async def async_step_zeroconf(
        self, discovery_info: ZeroconfServiceInfo
    ) -> ConfigFlowResult:
        """Handle a SHIP service discovered via zeroconf.

        Every SHIP device advertises _ship._tcp; only the bridge also serves
        gRPC, so probe the default gRPC port to tell them apart.
        """
        host = discovery_info.host
        ski = discovery_info.properties.get("ski", "")

        if not await self._async_port_reachable(host, DEFAULT_GRPC_PORT):
            return self.async_abort(reason="not_eebus_bridge")

        if ski:
            await self.async_set_unique_id(f"bridge_{ski}")
            self._abort_if_unique_id_configured()

        self._async_abort_entries_match({CONF_GRPC_HOST: host})

        self._host = host
        self._port = DEFAULT_GRPC_PORT
        self.context["title_placeholders"] = {"host": host}
        return await self.async_step_security()

    async def async_step_user(
        self, user_input: dict[str, Any] | None = None
    ) -> ConfigFlowResult:
        """Handle the initial step."""
        errors: dict[str, str] = {}

        if user_input is not None:
            self._host = user_input[CONF_GRPC_HOST]
            self._port = user_input[CONF_GRPC_PORT]

            return await self.async_step_security()

        return self.async_show_form(
            step_id="user",
            data_schema=STEP_USER_DATA_SCHEMA,
            errors=errors,
        )

    async def async_step_security(
        self, user_input: dict[str, Any] | None = None
    ) -> ConfigFlowResult:
        """Configure and verify the bridge transport security mode."""
        errors: dict[str, str] = {}
        if user_input is not None:
            self._security_mode = user_input[CONF_SECURITY_MODE]
            ca = str(user_input.get(CONF_TLS_CA_CERTIFICATE, "")).strip()
            token = str(user_input.get(CONF_AUTH_TOKEN, "")).strip()
            self._tls_ca_certificate = ca or None
            self._auth_token = token or None
            if self._security_mode == SECURITY_MODE_TLS_TOKEN:
                if not self._tls_ca_certificate:
                    errors[CONF_TLS_CA_CERTIFICATE] = "required"
                if not self._auth_token:
                    errors[CONF_AUTH_TOKEN] = "required"
            if not errors:
                probe = await self._async_probe_bridge(self._host, self._port)
                if probe.local_ski is not None:
                    self._local_ski = probe.local_ski
                    return await self.async_step_device()
                _LOGGER.warning(
                    "Failed to connect to EEBUS bridge during config flow at %s:%s",
                    self._host,
                    self._port,
                )
                errors["base"] = probe.error or ERROR_CANNOT_CONNECT

        return self.async_show_form(
            step_id="security",
            data_schema=_security_schema(
                self._security_mode, self._tls_ca_certificate or ""
            ),
            errors=errors,
        )

    async def async_step_device(
        self, user_input: dict[str, Any] | None = None
    ) -> ConfigFlowResult:
        """Handle device selection step."""
        errors: dict[str, str] = {}

        if user_input is not None:
            ski = normalize_ski(user_input[CONF_DEVICE_SKI])

            # The picker hides the bridge's own SKI, but custom_value=True lets a
            # user paste it by hand (e.g. the "Local SKI" line from the bridge
            # log). That SKI never resolves to a remote entity, so reject it
            # rather than create a permanently broken entry.
            if not is_valid_ski(ski):
                errors[CONF_DEVICE_SKI] = "invalid_ski"
            elif ski == normalize_ski(self._local_ski):
                errors[CONF_DEVICE_SKI] = "ski_is_local"
            else:
                await self.async_set_unique_id(ski)
                self._abort_if_unique_id_configured()

                return self.async_create_entry(
                    title=f"EEBUS {ski[:8]}",
                    data={
                        CONF_GRPC_HOST: self._host,
                        CONF_GRPC_PORT: self._port,
                        CONF_DEVICE_SKI: ski,
                        CONF_SECURITY_MODE: self._security_mode,
                        CONF_TLS_CA_CERTIFICATE: self._tls_ca_certificate,
                        CONF_AUTH_TOKEN: self._auth_token,
                    },
                )

        options = await self._async_list_discovered_skis()
        schema = vol.Schema(
            {
                vol.Required(CONF_DEVICE_SKI): SelectSelector(
                    SelectSelectorConfig(
                        options=options,
                        custom_value=True,
                        mode=SelectSelectorMode.DROPDOWN,
                    )
                ),
            }
        )
        return self.async_show_form(
            step_id="device", data_schema=schema, errors=errors
        )

    async def async_step_reconfigure(
        self, user_input: dict[str, Any] | None = None
    ) -> ConfigFlowResult:
        """Handle reconfiguration."""
        errors: dict[str, str] = {}

        if user_input is not None:
            self._host = user_input[CONF_GRPC_HOST]
            self._port = user_input[CONF_GRPC_PORT]
            return await self.async_step_reconfigure_security()

        entry = self._get_reconfigure_entry()
        return self.async_show_form(
            step_id="reconfigure",
            data_schema=vol.Schema(
                {
                    vol.Required(CONF_GRPC_HOST, default=entry.data.get(CONF_GRPC_HOST, "")): str,
                    vol.Required(CONF_GRPC_PORT, default=entry.data.get(CONF_GRPC_PORT, DEFAULT_GRPC_PORT)): int,
                }
            ),
            errors=errors,
        )

    async def async_step_reconfigure_security(
        self, user_input: dict[str, Any] | None = None
    ) -> ConfigFlowResult:
        """Update and verify transport credentials during reconfiguration."""
        entry = self._get_reconfigure_entry()
        current_mode = entry.data.get(CONF_SECURITY_MODE, SECURITY_MODE_LOOPBACK)
        current_ca = entry.data.get(CONF_TLS_CA_CERTIFICATE) or ""
        errors: dict[str, str] = {}

        if user_input is not None:
            self._security_mode = user_input[CONF_SECURITY_MODE]
            ca = str(user_input.get(CONF_TLS_CA_CERTIFICATE, "")).strip()
            token = str(user_input.get(CONF_AUTH_TOKEN, "")).strip()
            self._tls_ca_certificate = ca or None
            self._auth_token = token or entry.data.get(CONF_AUTH_TOKEN)
            if self._security_mode == SECURITY_MODE_TLS_TOKEN:
                if not self._tls_ca_certificate:
                    errors[CONF_TLS_CA_CERTIFICATE] = "required"
                if not self._auth_token:
                    errors[CONF_AUTH_TOKEN] = "required"
            else:
                self._tls_ca_certificate = None
                self._auth_token = None

            if not errors:
                probe = await self._async_probe_bridge(self._host, self._port)
                if probe.local_ski is not None:
                    return self.async_update_reload_and_abort(
                        entry,
                        data_updates={
                            CONF_GRPC_HOST: self._host,
                            CONF_GRPC_PORT: self._port,
                            CONF_SECURITY_MODE: self._security_mode,
                            CONF_TLS_CA_CERTIFICATE: self._tls_ca_certificate,
                            CONF_AUTH_TOKEN: self._auth_token,
                        },
                    )
                _LOGGER.warning(
                    "Failed to connect to EEBUS bridge during reconfigure at %s:%s",
                    self._host,
                    self._port,
                )
                errors["base"] = probe.error or ERROR_CANNOT_CONNECT

        form_mode = self._security_mode if user_input is not None else current_mode
        form_ca = (
            (self._tls_ca_certificate or "") if user_input is not None else current_ca
        )
        return self.async_show_form(
            step_id="reconfigure_security",
            data_schema=_security_schema(form_mode, form_ca),
            errors=errors,
        )

    async def async_step_reauth(
        self, entry_data: dict[str, Any]
    ) -> ConfigFlowResult:
        """Start credential replacement after an authentication failure."""
        self._host = entry_data[CONF_GRPC_HOST]
        self._port = entry_data[CONF_GRPC_PORT]
        self._security_mode = entry_data.get(
            CONF_SECURITY_MODE, SECURITY_MODE_LOOPBACK
        )
        self._tls_ca_certificate = entry_data.get(CONF_TLS_CA_CERTIFICATE)
        return await self.async_step_reauth_confirm()

    async def async_step_reauth_confirm(
        self, user_input: dict[str, Any] | None = None
    ) -> ConfigFlowResult:
        """Verify replacement TLS credentials and reload the entry."""
        errors: dict[str, str] = {}
        if user_input is not None:
            self._tls_ca_certificate = str(
                user_input[CONF_TLS_CA_CERTIFICATE]
            ).strip()
            self._auth_token = str(user_input[CONF_AUTH_TOKEN]).strip()
            probe = await self._async_probe_bridge(self._host, self._port)
            if probe.local_ski is not None:
                return self.async_update_reload_and_abort(
                    self._get_reauth_entry(),
                    data_updates={
                        CONF_TLS_CA_CERTIFICATE: self._tls_ca_certificate,
                        CONF_AUTH_TOKEN: self._auth_token,
                    },
                    reason="reauth_successful",
                )
            errors["base"] = probe.error or ERROR_CANNOT_CONNECT

        return self.async_show_form(
            step_id="reauth_confirm",
            data_schema=_reauth_schema(self._tls_ca_certificate or ""),
            errors=errors,
        )


class EebusOptionsFlow(OptionsFlow):
    """Map Home Assistant sensors to the bridge's EEBUS provider use cases.

    Grid sensors feed the MGCP provider so a heat pump (e.g. Vaillant VR940) can
    run PV-surplus optimisation from HA's grid data (grid power, negative =
    export). PV and battery sensors feed the VAPD/VABD display providers so the
    device can show PV/battery data. Each section's power sensor is required to
    enable that provider; the rest are optional. Leaving a power sensor empty
    disables that push.
    """

    async def async_step_init(
        self, user_input: dict[str, Any] | None = None
    ) -> ConfigFlowResult:
        """Handle the sensor mapping step."""
        if user_input is not None:
            # Drop empty selections so cleared fields are removed from options.
            data = {key: value for key, value in user_input.items() if value}
            return self.async_create_entry(title="", data=data)

        options = self.config_entry.options

        def _entity_selector(device_class: str) -> EntitySelector:
            return EntitySelector(
                EntitySelectorConfig(domain="sensor", device_class=device_class)
            )

        def _field(key: str) -> dict[str, Any]:
            return {"suggested_value": options.get(key)}

        schema = vol.Schema(
            {
                vol.Optional(
                    CONF_GRID_POWER_ENTITY,
                    description=_field(CONF_GRID_POWER_ENTITY),
                ): _entity_selector("power"),
                vol.Optional(
                    CONF_GRID_FEED_IN_ENERGY_ENTITY,
                    description=_field(CONF_GRID_FEED_IN_ENERGY_ENTITY),
                ): _entity_selector("energy"),
                vol.Optional(
                    CONF_GRID_CONSUMPTION_ENERGY_ENTITY,
                    description=_field(CONF_GRID_CONSUMPTION_ENERGY_ENTITY),
                ): _entity_selector("energy"),
                vol.Optional(
                    CONF_PV_POWER_ENTITY,
                    description=_field(CONF_PV_POWER_ENTITY),
                ): _entity_selector("power"),
                vol.Optional(
                    CONF_PV_YIELD_ENERGY_ENTITY,
                    description=_field(CONF_PV_YIELD_ENERGY_ENTITY),
                ): _entity_selector("energy"),
                vol.Optional(
                    CONF_PV_PEAK_POWER_ENTITY,
                    description=_field(CONF_PV_PEAK_POWER_ENTITY),
                ): _entity_selector("power"),
                vol.Optional(
                    CONF_BATTERY_POWER_ENTITY,
                    description=_field(CONF_BATTERY_POWER_ENTITY),
                ): _entity_selector("power"),
                vol.Optional(
                    CONF_BATTERY_CHARGED_ENERGY_ENTITY,
                    description=_field(CONF_BATTERY_CHARGED_ENERGY_ENTITY),
                ): _entity_selector("energy"),
                vol.Optional(
                    CONF_BATTERY_DISCHARGED_ENERGY_ENTITY,
                    description=_field(CONF_BATTERY_DISCHARGED_ENERGY_ENTITY),
                ): _entity_selector("energy"),
                vol.Optional(
                    CONF_BATTERY_SOC_ENTITY,
                    description=_field(CONF_BATTERY_SOC_ENTITY),
                ): _entity_selector("battery"),
            }
        )
        return self.async_show_form(step_id="init", data_schema=schema)
