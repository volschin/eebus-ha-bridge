"""Config flow for EEBUS integration."""

from __future__ import annotations

import logging
from typing import Any

import grpc
import grpc.aio
import voluptuous as vol

from homeassistant.config_entries import ConfigFlow, ConfigFlowResult
from homeassistant.helpers.selector import (
    SelectOptionDict,
    SelectSelector,
    SelectSelectorConfig,
    SelectSelectorMode,
)
from homeassistant.helpers.service_info.zeroconf import ZeroconfServiceInfo

from .const import (
    CONF_DEVICE_SKI,
    CONF_GRPC_HOST,
    CONF_GRPC_PORT,
    DEFAULT_GRPC_PORT,
    DOMAIN,
)

_LOGGER = logging.getLogger(__name__)

CONFIG_RPC_TIMEOUT = 8

STEP_USER_DATA_SCHEMA = vol.Schema(
    {
        vol.Required(CONF_GRPC_HOST): str,
        vol.Required(CONF_GRPC_PORT, default=DEFAULT_GRPC_PORT): int,
    }
)


class EebusConfigFlow(ConfigFlow, domain=DOMAIN):
    """Handle a config flow for EEBUS."""

    VERSION = 1
    DOMAIN = DOMAIN

    def __init__(self) -> None:
        """Initialize."""
        self._host: str = ""
        self._port: int = DEFAULT_GRPC_PORT
        self._local_ski: str = ""

    async def _async_probe_bridge(self, host: str, port: int) -> str | None:
        """Check bridge reachability; return its local SKI or None."""
        channel = grpc.aio.insecure_channel(f"{host}:{port}")
        try:
            from . import proto_stubs

            stub = proto_stubs.DeviceServiceStub(channel)
            status = await stub.GetStatus(
                proto_stubs.Empty(), timeout=CONFIG_RPC_TIMEOUT
            )
            return status.local_ski
        except Exception:  # noqa: BLE001
            _LOGGER.debug(
                "No EEBUS bridge reachable at %s:%s", host, port, exc_info=True
            )
            return None
        finally:
            await channel.close()

    async def _async_list_discovered_skis(self) -> list[SelectOptionDict]:
        """Fetch discovered devices from the bridge for the SKI picker."""
        channel = grpc.aio.insecure_channel(f"{self._host}:{self._port}")
        try:
            from . import proto_stubs

            stub = proto_stubs.DeviceServiceStub(channel)
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
        except Exception:  # noqa: BLE001
            _LOGGER.debug("Listing discovered devices failed", exc_info=True)
            return []
        finally:
            await channel.close()

    async def async_step_zeroconf(
        self, discovery_info: ZeroconfServiceInfo
    ) -> ConfigFlowResult:
        """Handle a SHIP service discovered via zeroconf.

        Every SHIP device advertises _ship._tcp; only the bridge also serves
        gRPC, so probe the default gRPC port to tell them apart.
        """
        host = discovery_info.host
        ski = discovery_info.properties.get("ski", "")

        if ski:
            await self.async_set_unique_id(f"bridge_{ski}")
            self._abort_if_unique_id_configured()

        local_ski = await self._async_probe_bridge(host, DEFAULT_GRPC_PORT)
        if local_ski is None:
            return self.async_abort(reason="not_eebus_bridge")

        self._async_abort_entries_match({CONF_GRPC_HOST: host})

        self._host = host
        self._port = DEFAULT_GRPC_PORT
        self._local_ski = local_ski
        self.context["title_placeholders"] = {"host": host}
        return await self.async_step_device()

    async def async_step_user(
        self, user_input: dict[str, Any] | None = None
    ) -> ConfigFlowResult:
        """Handle the initial step."""
        errors: dict[str, str] = {}

        if user_input is not None:
            self._host = user_input[CONF_GRPC_HOST]
            self._port = user_input[CONF_GRPC_PORT]

            local_ski = await self._async_probe_bridge(self._host, self._port)
            if local_ski is not None:
                self._local_ski = local_ski
                return await self.async_step_device()
            _LOGGER.warning(
                "Failed to connect to EEBUS bridge during config flow at %s:%s",
                self._host,
                self._port,
            )
            errors["base"] = "cannot_connect"

        return self.async_show_form(
            step_id="user",
            data_schema=STEP_USER_DATA_SCHEMA,
            errors=errors,
        )

    async def async_step_device(
        self, user_input: dict[str, Any] | None = None
    ) -> ConfigFlowResult:
        """Handle device selection step."""
        if user_input is not None:
            ski = user_input[CONF_DEVICE_SKI].strip()
            await self.async_set_unique_id(ski)
            self._abort_if_unique_id_configured()

            return self.async_create_entry(
                title=f"EEBUS {ski[:8]}",
                data={
                    CONF_GRPC_HOST: self._host,
                    CONF_GRPC_PORT: self._port,
                    CONF_DEVICE_SKI: ski,
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
        return self.async_show_form(step_id="device", data_schema=schema)

    async def async_step_reconfigure(
        self, user_input: dict[str, Any] | None = None
    ) -> ConfigFlowResult:
        """Handle reconfiguration."""
        errors: dict[str, str] = {}

        if user_input is not None:
            local_ski = await self._async_probe_bridge(
                user_input[CONF_GRPC_HOST], user_input[CONF_GRPC_PORT]
            )
            if local_ski is not None:
                return self.async_update_reload_and_abort(
                    self._get_reconfigure_entry(),
                    data_updates={
                        CONF_GRPC_HOST: user_input[CONF_GRPC_HOST],
                        CONF_GRPC_PORT: user_input[CONF_GRPC_PORT],
                    },
                )
            _LOGGER.warning(
                "Failed to connect to EEBUS bridge during reconfigure at %s:%s",
                user_input[CONF_GRPC_HOST],
                user_input[CONF_GRPC_PORT],
            )
            errors["base"] = "cannot_connect"

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
