package eebus

import (
	"sort"
	"time"
)

// Capability identifies an optional device-facing bridge feature.
type Capability uint8

const (
	CapabilityMonitoring Capability = iota + 1
	CapabilityLPC
	CapabilityFailsafe
	CapabilityHeartbeat
	CapabilityOHPCF
	CapabilityDHW
	CapabilityDHWSystemFunction
	CapabilityRoomHeating
)

var AllCapabilities = [...]Capability{
	CapabilityMonitoring,
	CapabilityLPC,
	CapabilityFailsafe,
	CapabilityHeartbeat,
	CapabilityOHPCF,
	CapabilityDHW,
	CapabilityDHWSystemFunction,
	CapabilityRoomHeating,
}

type CapabilityState uint8

const (
	CapabilityStateUnknown CapabilityState = iota
	CapabilityStateAvailable
	CapabilityStateTemporarilyUnavailable
	CapabilityStateUnsupported
)

type CapabilityReason uint8

const (
	CapabilityReasonUnspecified CapabilityReason = iota
	CapabilityReasonLocalDisabled
	CapabilityReasonRemoteNotAdvertised
	CapabilityReasonEntityNotBound
	CapabilityReasonReadFailed
	CapabilityReasonDeviceDisconnected
)

type DeviceCapability struct {
	ID          Capability
	State       CapabilityState
	Reason      CapabilityReason
	LastChanged time.Time
}

func (r *DeviceRegistry) SetLocalCapabilityEnabled(capability Capability, enabled bool) {
	r.capabilities.mu.Lock()
	defer r.capabilities.mu.Unlock()
	r.capabilities.localCapabilities[capability] = enabled
	for ski := range r.capabilities.entries {
		if enabled {
			r.setCapabilityLocked(ski, capability, CapabilityStateUnknown, CapabilityReasonUnspecified)
		} else {
			r.setCapabilityLocked(ski, capability, CapabilityStateUnsupported, CapabilityReasonLocalDisabled)
		}
	}
}

func (r *DeviceRegistry) SetCapability(ski string, capability Capability, state CapabilityState, reason CapabilityReason) {
	ski = NormalizeSKI(ski)
	r.lifecycle.RLock()
	defer r.lifecycle.RUnlock()
	if r.removedLocked(ski) {
		return
	}
	r.capabilities.mu.Lock()
	defer r.capabilities.mu.Unlock()
	r.setCapabilityLocked(ski, capability, state, reason)
}

func (r *DeviceRegistry) setCapabilityLocked(ski string, capability Capability, state CapabilityState, reason CapabilityReason) {
	if enabled, ok := r.capabilities.localCapabilities[capability]; ok && !enabled {
		state = CapabilityStateUnsupported
		reason = CapabilityReasonLocalDisabled
	}
	entries := r.ensureCapabilitiesLocked(ski)
	current := entries[capability]
	if current.State == state && current.Reason == reason {
		return
	}
	entries[capability] = DeviceCapability{ID: capability, State: state, Reason: reason, LastChanged: r.clock.Now()}
}

func (r *DeviceRegistry) ensureCapabilitiesLocked(ski string) map[Capability]DeviceCapability {
	entries, ok := r.capabilities.entries[ski]
	if !ok {
		entries = make(map[Capability]DeviceCapability, len(AllCapabilities))
		now := r.clock.Now()
		for _, capability := range AllCapabilities {
			state := CapabilityStateUnknown
			reason := CapabilityReasonUnspecified
			if enabled, configured := r.capabilities.localCapabilities[capability]; configured && !enabled {
				state = CapabilityStateUnsupported
				reason = CapabilityReasonLocalDisabled
			}
			entries[capability] = DeviceCapability{ID: capability, State: state, Reason: reason, LastChanged: now}
		}
		r.capabilities.entries[ski] = entries
	}
	return entries
}

func (r *DeviceRegistry) ensureCapabilities(ski string) {
	r.capabilities.mu.Lock()
	r.ensureCapabilitiesLocked(NormalizeSKI(ski))
	r.capabilities.mu.Unlock()
}

func (r *DeviceRegistry) markCapabilitiesConnected(ski string) {
	r.capabilities.mu.Lock()
	defer r.capabilities.mu.Unlock()
	entries := r.ensureCapabilitiesLocked(ski)
	for capability, entry := range entries {
		if entry.Reason == CapabilityReasonDeviceDisconnected {
			r.setCapabilityLocked(ski, capability, CapabilityStateUnknown, CapabilityReasonEntityNotBound)
		}
	}
}

func (r *DeviceRegistry) markCapabilitiesDisconnected(ski string) {
	r.capabilities.mu.Lock()
	defer r.capabilities.mu.Unlock()
	for capability, entry := range r.ensureCapabilitiesLocked(ski) {
		if entry.State != CapabilityStateUnsupported {
			r.setCapabilityLocked(ski, capability, CapabilityStateTemporarilyUnavailable, CapabilityReasonDeviceDisconnected)
		}
	}
}

func (r *DeviceRegistry) deviceDisconnected(ski string) bool {
	r.health.mu.RLock()
	defer r.health.mu.RUnlock()
	connection, known := r.health.monitoring[ski]
	return known && !connection.connected
}

func (r *DeviceRegistry) deviceHasEntities(ski string) bool {
	r.catalog.mu.RLock()
	defer r.catalog.mu.RUnlock()
	info, known := r.catalog.devices[ski]
	return known && (len(info.RemoteEntities) > 0 || len(info.Entities) > 0)
}

func (r *DeviceRegistry) DeviceCapabilities(ski string) ([]DeviceCapability, bool) {
	r.capabilities.mu.RLock()
	defer r.capabilities.mu.RUnlock()
	entries, ok := r.capabilities.entries[NormalizeSKI(ski)]
	if !ok {
		return nil, false
	}
	result := make([]DeviceCapability, 0, len(entries))
	for _, entry := range entries {
		result = append(result, entry)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, true
}

// RecordCapabilitySupport is called from use-case support callbacks. A callback
// with an entity proves remote advertisement; one without an entity explicitly
// revokes support until a later support event says otherwise.
func (r *DeviceRegistry) RecordCapabilitySupport(ski string, capability Capability, advertised bool) {
	ski = NormalizeSKI(ski)
	r.lifecycle.RLock()
	defer r.lifecycle.RUnlock()
	if r.removedLocked(ski) {
		return
	}
	disconnected := r.deviceDisconnected(ski)
	r.capabilities.mu.Lock()
	defer r.capabilities.mu.Unlock()
	r.recordCapabilitySupportLocked(ski, capability, advertised, disconnected)
}

// RecordCapabilitySourceSupport combines the support reported by multiple use
// cases that contribute to one aggregate capability. The aggregate is remotely
// unsupported only after every source seen for it currently reports false.
func (r *DeviceRegistry) RecordCapabilitySourceSupport(
	ski string,
	capability Capability,
	source string,
	advertised bool,
) {
	ski = NormalizeSKI(ski)
	r.lifecycle.RLock()
	defer r.lifecycle.RUnlock()
	if r.removedLocked(ski) {
		return
	}
	disconnected := r.deviceDisconnected(ski)
	r.capabilities.mu.Lock()
	defer r.capabilities.mu.Unlock()
	byCapability, ok := r.capabilities.support[ski]
	if !ok {
		byCapability = make(map[Capability]map[string]bool)
		r.capabilities.support[ski] = byCapability
	}
	sources, ok := byCapability[capability]
	if !ok {
		sources = make(map[string]bool)
		byCapability[capability] = sources
	}
	sources[source] = advertised
	for _, supported := range sources {
		if supported {
			r.recordCapabilitySupportLocked(ski, capability, true, disconnected)
			return
		}
	}
	r.recordCapabilitySupportLocked(ski, capability, false, disconnected)
}

func (r *DeviceRegistry) recordCapabilitySupportLocked(ski string, capability Capability, advertised, disconnected bool) {
	if disconnected {
		current := r.ensureCapabilitiesLocked(ski)[capability]
		if current.Reason != CapabilityReasonLocalDisabled && current.State != CapabilityStateUnsupported {
			r.setCapabilityLocked(ski, capability, CapabilityStateTemporarilyUnavailable, CapabilityReasonDeviceDisconnected)
		}
		return
	}
	if advertised {
		r.setCapabilityLocked(ski, capability, CapabilityStateUnknown, CapabilityReasonUnspecified)
		return
	}
	r.setCapabilityLocked(ski, capability, CapabilityStateUnsupported, CapabilityReasonRemoteNotAdvertised)
}

func (r *DeviceRegistry) RecordCapabilityRead(ski string, capability Capability, err error) {
	ski = NormalizeSKI(ski)
	r.lifecycle.RLock()
	defer r.lifecycle.RUnlock()
	if r.removedLocked(ski) {
		return
	}
	disconnected := r.deviceDisconnected(ski)
	r.capabilities.mu.Lock()
	defer r.capabilities.mu.Unlock()
	if r.ensureCapabilitiesLocked(ski)[capability].State == CapabilityStateUnsupported {
		return
	}
	if disconnected {
		r.setCapabilityLocked(ski, capability, CapabilityStateTemporarilyUnavailable, CapabilityReasonDeviceDisconnected)
		return
	}
	if err == nil {
		r.setCapabilityLocked(ski, capability, CapabilityStateAvailable, CapabilityReasonUnspecified)
		return
	}
	r.setCapabilityLocked(ski, capability, CapabilityStateTemporarilyUnavailable, CapabilityReasonReadFailed)
}

func (r *DeviceRegistry) RecordCapabilityEntityNotBound(ski string, capability Capability) {
	ski = NormalizeSKI(ski)
	r.lifecycle.RLock()
	defer r.lifecycle.RUnlock()
	if r.removedLocked(ski) {
		return
	}
	disconnected := r.deviceDisconnected(ski)
	r.capabilities.mu.Lock()
	defer r.capabilities.mu.Unlock()
	if r.ensureCapabilitiesLocked(ski)[capability].State == CapabilityStateUnsupported {
		return
	}
	if disconnected {
		r.setCapabilityLocked(ski, capability, CapabilityStateTemporarilyUnavailable, CapabilityReasonDeviceDisconnected)
		return
	}
	r.setCapabilityLocked(ski, capability, CapabilityStateTemporarilyUnavailable, CapabilityReasonEntityNotBound)
}

// RecordCapabilityMissingEntity distinguishes a known remote device that does
// not advertise this use case from a device whose entities are not bound yet.
func (r *DeviceRegistry) RecordCapabilityMissingEntity(ski string, capability Capability) {
	ski = NormalizeSKI(ski)
	r.lifecycle.RLock()
	defer r.lifecycle.RUnlock()
	if r.removedLocked(ski) {
		return
	}
	disconnected := r.deviceDisconnected(ski)
	hasEntities := r.deviceHasEntities(ski)
	r.capabilities.mu.Lock()
	defer r.capabilities.mu.Unlock()
	if r.ensureCapabilitiesLocked(ski)[capability].State == CapabilityStateUnsupported {
		return
	}
	if disconnected {
		r.setCapabilityLocked(ski, capability, CapabilityStateTemporarilyUnavailable, CapabilityReasonDeviceDisconnected)
		return
	}
	if hasEntities {
		r.setCapabilityLocked(ski, capability, CapabilityStateUnsupported, CapabilityReasonRemoteNotAdvertised)
		return
	}
	r.setCapabilityLocked(ski, capability, CapabilityStateTemporarilyUnavailable, CapabilityReasonEntityNotBound)
}
