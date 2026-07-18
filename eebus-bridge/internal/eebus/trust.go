package eebus

import "errors"

type remoteTrustService interface {
	RegisterRemoteSKI(ski string)
	UnregisterRemoteSKI(ski string)
}

// TrustController applies pairing commands synchronously and maintains the
// bridge-owned device state that accompanies those commands.
type TrustController struct {
	bridge   remoteTrustService
	registry *DeviceRegistry
	bus      *EventBus
}

func NewTrustController(bridge *BridgeService, registry *DeviceRegistry, bus *EventBus) *TrustController {
	if bridge == nil {
		return &TrustController{registry: registry, bus: bus}
	}
	return &TrustController{bridge: bridge, registry: registry, bus: bus}
}

func (c *TrustController) RegisterSKI(ski string) error {
	if c == nil || c.bridge == nil {
		return errors.New("bridge service is required")
	}
	c.bridge.RegisterRemoteSKI(ski)
	if c.registry != nil {
		c.registry.MarkTrusted(ski)
	}
	return nil
}

func (c *TrustController) UnregisterSKI(ski string) error {
	if c == nil || c.bridge == nil {
		return errors.New("bridge service is required")
	}
	if c.registry == nil {
		return errors.New("device registry is required")
	}
	if c.bus == nil {
		return errors.New("event bus is required")
	}

	c.bridge.UnregisterRemoteSKI(ski)
	// eebus-go's UnregisterRemoteService only notifies via
	// ServicePairingDetailUpdate, not ServiceAutoTrustRemoved, so the registry
	// is cleared explicitly here rather than relying on a callback (cf.
	// ServiceAutoTrustRemoved in callbacks.go, which handles remote-initiated
	// revocation).
	c.registry.MarkUntrusted(ski)
	c.registry.RemoveDevice(ski)
	c.bus.Publish(Event{SKI: ski, Type: EventTypeDeviceTrustRemoved})
	return nil
}
