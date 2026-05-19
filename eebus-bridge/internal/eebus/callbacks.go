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
	discoveredSvcs []shipapi.RemoteService
	pairingStates  map[string]*shipapi.ConnectionStateDetail
	debugEvents    bool
}

// NewCallbacks creates a new Callbacks instance backed by the given EventBus.
func NewCallbacks(bus *EventBus, debugEvents bool) *Callbacks {
	return &Callbacks{
		bus:           bus,
		pairingStates: make(map[string]*shipapi.ConnectionStateDetail),
		debugEvents:   debugEvents,
	}
}

// Compile-time assertion that Callbacks implements api.ServiceReaderInterface.
var _ api.ServiceReaderInterface = (*Callbacks)(nil)

// RemoteSKIConnected is called when a remote SKI connects.
func (c *Callbacks) RemoteSKIConnected(service api.ServiceInterface, ski string) {
	if c.debugEvents {
		log.Printf("[DEBUG] EEBUS callback: remote SKI connected: ski=%s", ski)
	}

	c.bus.Publish(Event{
		SKI:  ski,
		Type: "device.connected",
	})
}

// RemoteSKIDisconnected is called when a remote SKI disconnects.
func (c *Callbacks) RemoteSKIDisconnected(service api.ServiceInterface, ski string) {
	if c.debugEvents {
		log.Printf("[DEBUG] EEBUS callback: remote SKI disconnected: ski=%s", ski)
	}

	c.bus.Publish(Event{
		SKI:  ski,
		Type: "device.disconnected",
	})
}

// VisibleRemoteServicesUpdated is called when the list of visible remote services changes.
func (c *Callbacks) VisibleRemoteServicesUpdated(service api.ServiceInterface, entries []shipapi.RemoteService) {
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

// ServiceShipIDUpdate is called when the SHIP ID of a remote service is reported.
func (c *Callbacks) ServiceShipIDUpdate(ski string, shipID string) {
	// no-op: SHIP IDs are informational only in this bridge
}

// ServicePairingDetailUpdate is called when the pairing state of a remote service changes.
func (c *Callbacks) ServicePairingDetailUpdate(ski string, detail *shipapi.ConnectionStateDetail) {
	if c.debugEvents {
		if detail != nil {
			log.Printf("[DEBUG] EEBUS callback: pairing detail updated: ski=%s state=%v", ski, detail.State())
		} else {
			log.Printf("[DEBUG] EEBUS callback: pairing detail updated: ski=%s state=<nil>", ski)
		}
	}

	c.mu.Lock()
	c.pairingStates[ski] = detail
	c.mu.Unlock()

	c.bus.Publish(Event{
		SKI:  ski,
		Type: "pairing.updated",
	})
}

// AllowWaitingForTrust is called by the SHIP layer to determine whether to wait
// for user trust before completing a connection. Always returns true so that
// incoming pairing requests can be accepted via the bridge API.
func (c *Callbacks) AllowWaitingForTrust(ski string) bool {
	return true
}

// DiscoveredServices returns a snapshot of the currently visible remote services.
func (c *Callbacks) DiscoveredServices() []shipapi.RemoteService {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make([]shipapi.RemoteService, len(c.discoveredSvcs))
	copy(result, c.discoveredSvcs)
	return result
}

// PairingState returns the current pairing state for the given SKI, or nil if unknown.
func (c *Callbacks) PairingState(ski string) *shipapi.ConnectionStateDetail {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.pairingStates[ski]
}
