package eebus

import (
	"log"
	"sync"

	"github.com/enbility/eebus-go/api"
	shipapi "github.com/enbility/ship-go/api"
)

// Callbacks implements api.ServiceReaderInterface and dispatches events to the EventBus.
type Callbacks struct {
	bus            *EventBus
	mu             sync.RWMutex
	discoveredSvcs []shipapi.RemoteMdnsService
	debugEvents    bool
}

// NewCallbacks creates a new Callbacks instance backed by the given EventBus.
func NewCallbacks(bus *EventBus, debugEvents bool) *Callbacks {
	return &Callbacks{
		bus:         bus,
		debugEvents: debugEvents,
	}
}

// Compile-time assertion that Callbacks implements api.ServiceReaderInterface.
var _ api.ServiceReaderInterface = (*Callbacks)(nil)

// RemoteServiceConnected is called when a remote service connects.
func (c *Callbacks) RemoteServiceConnected(_ api.ServiceInterface, identity shipapi.ServiceIdentity) {
	ski := NormalizeSKI(identity.SKI)
	if c.debugEvents {
		log.Printf("[DEBUG] EEBUS callback: remote service connected: ski=%s", ski)
	}

	c.bus.Publish(Event{
		SKI:  ski,
		Type: "device.connected",
	})
}

// RemoteServiceDisconnected is called when a remote service disconnects.
func (c *Callbacks) RemoteServiceDisconnected(_ api.ServiceInterface, identity shipapi.ServiceIdentity) {
	ski := NormalizeSKI(identity.SKI)
	if c.debugEvents {
		log.Printf("[DEBUG] EEBUS callback: remote service disconnected: ski=%s", ski)
	}

	c.bus.Publish(Event{
		SKI:  ski,
		Type: "device.disconnected",
	})
}

// VisibleRemoteMdnsServicesUpdated is called when the list of visible remote mDNS services changes.
func (c *Callbacks) VisibleRemoteMdnsServicesUpdated(_ api.ServiceInterface, entries []shipapi.RemoteMdnsService) {
	if c.debugEvents {
		log.Printf("[DEBUG] EEBUS callback: visible remote services updated: count=%d", len(entries))
	}

	c.mu.Lock()
	c.discoveredSvcs = entries
	c.mu.Unlock()

	c.bus.Publish(Event{
		Type: "discovery.updated",
	})
}

// ServiceUpdated is called when a remote service's discovered details change.
func (c *Callbacks) ServiceUpdated(_ shipapi.ServiceIdentity) {
	// no-op: service detail updates are informational only in this bridge
}

// ServicePairingDetailUpdate is called when the pairing state of a remote service changes.
func (c *Callbacks) ServicePairingDetailUpdate(identity shipapi.ServiceIdentity, detail *shipapi.ConnectionStateDetail) {
	ski := NormalizeSKI(identity.SKI)
	if c.debugEvents {
		if detail != nil {
			log.Printf("[DEBUG] EEBUS callback: pairing detail updated: ski=%s state=%v", ski, detail.State())
		} else {
			log.Printf("[DEBUG] EEBUS callback: pairing detail updated: ski=%s state=<nil>", ski)
		}
	}

	c.bus.Publish(Event{
		SKI:  ski,
		Type: "pairing.updated",
	})
}

// ServiceAutoTrusted is called when a device is automatically trusted via SHIP pairing.
func (c *Callbacks) ServiceAutoTrusted(_ api.ServiceInterface, identity shipapi.ServiceIdentity) {
	if c.debugEvents {
		log.Printf("[DEBUG] EEBUS callback: service auto-trusted: ski=%s", NormalizeSKI(identity.SKI))
	}
}

// ServiceAutoTrustFailed is called when SHIP pairing fails for a device.
func (c *Callbacks) ServiceAutoTrustFailed(_ api.ServiceInterface, identity shipapi.ServiceIdentity, reason error) {
	if c.debugEvents {
		log.Printf("[DEBUG] EEBUS callback: service auto-trust failed: ski=%s reason=%v", NormalizeSKI(identity.SKI), reason)
	}
}

// ServiceAutoTrustRemoved is called when device trust is automatically removed.
func (c *Callbacks) ServiceAutoTrustRemoved(_ api.ServiceInterface, identity shipapi.ServiceIdentity, reason string) {
	if c.debugEvents {
		log.Printf("[DEBUG] EEBUS callback: service auto-trust removed: ski=%s reason=%s", NormalizeSKI(identity.SKI), reason)
	}
}

// DiscoveredServices returns a snapshot of the currently visible remote services.
func (c *Callbacks) DiscoveredServices() []shipapi.RemoteMdnsService {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make([]shipapi.RemoteMdnsService, len(c.discoveredSvcs))
	copy(result, c.discoveredSvcs)
	return result
}
