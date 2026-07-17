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
	r.mu.Lock()
	defer r.mu.Unlock()
	r.localCapabilities[capability] = enabled
	for ski := range r.capabilities {
		if enabled {
			r.setCapabilityLocked(ski, capability, CapabilityStateUnknown, CapabilityReasonUnspecified)
		} else {
			r.setCapabilityLocked(ski, capability, CapabilityStateUnsupported, CapabilityReasonLocalDisabled)
		}
	}
}

func (r *DeviceRegistry) SetCapability(ski string, capability Capability, state CapabilityState, reason CapabilityReason) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.setCapabilityLocked(NormalizeSKI(ski), capability, state, reason)
}

func (r *DeviceRegistry) setCapabilityLocked(ski string, capability Capability, state CapabilityState, reason CapabilityReason) {
	if enabled, ok := r.localCapabilities[capability]; ok && !enabled {
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
	entries, ok := r.capabilities[ski]
	if !ok {
		entries = make(map[Capability]DeviceCapability, len(AllCapabilities))
		now := r.clock.Now()
		for _, capability := range AllCapabilities {
			state := CapabilityStateUnknown
			reason := CapabilityReasonUnspecified
			if enabled, configured := r.localCapabilities[capability]; configured && !enabled {
				state = CapabilityStateUnsupported
				reason = CapabilityReasonLocalDisabled
			}
			entries[capability] = DeviceCapability{ID: capability, State: state, Reason: reason, LastChanged: now}
		}
		r.capabilities[ski] = entries
	}
	return entries
}

func (r *DeviceRegistry) DeviceCapabilities(ski string) ([]DeviceCapability, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entries, ok := r.capabilities[NormalizeSKI(ski)]
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
	r.mu.Lock()
	defer r.mu.Unlock()
	ski = NormalizeSKI(ski)
	r.recordCapabilitySupportLocked(ski, capability, advertised)
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
	r.mu.Lock()
	defer r.mu.Unlock()
	ski = NormalizeSKI(ski)
	byCapability, ok := r.capabilitySupport[ski]
	if !ok {
		byCapability = make(map[Capability]map[string]bool)
		r.capabilitySupport[ski] = byCapability
	}
	sources, ok := byCapability[capability]
	if !ok {
		sources = make(map[string]bool)
		byCapability[capability] = sources
	}
	sources[source] = advertised
	for _, supported := range sources {
		if supported {
			r.recordCapabilitySupportLocked(ski, capability, true)
			return
		}
	}
	r.recordCapabilitySupportLocked(ski, capability, false)
}

func (r *DeviceRegistry) recordCapabilitySupportLocked(ski string, capability Capability, advertised bool) {
	if connection, known := r.monitoring[ski]; known && !connection.connected {
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
	r.mu.Lock()
	defer r.mu.Unlock()
	ski = NormalizeSKI(ski)
	if r.ensureCapabilitiesLocked(ski)[capability].State == CapabilityStateUnsupported {
		return
	}
	if connection, known := r.monitoring[ski]; known && !connection.connected {
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
	r.mu.Lock()
	defer r.mu.Unlock()
	ski = NormalizeSKI(ski)
	if r.ensureCapabilitiesLocked(ski)[capability].State == CapabilityStateUnsupported {
		return
	}
	if connection, known := r.monitoring[ski]; known && !connection.connected {
		r.setCapabilityLocked(ski, capability, CapabilityStateTemporarilyUnavailable, CapabilityReasonDeviceDisconnected)
		return
	}
	r.setCapabilityLocked(ski, capability, CapabilityStateTemporarilyUnavailable, CapabilityReasonEntityNotBound)
}

// RecordCapabilityMissingEntity distinguishes a known remote device that does
// not advertise this use case from a device whose entities are not bound yet.
func (r *DeviceRegistry) RecordCapabilityMissingEntity(ski string, capability Capability) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ski = NormalizeSKI(ski)
	if r.ensureCapabilitiesLocked(ski)[capability].State == CapabilityStateUnsupported {
		return
	}
	if connection, known := r.monitoring[ski]; known && !connection.connected {
		r.setCapabilityLocked(ski, capability, CapabilityStateTemporarilyUnavailable, CapabilityReasonDeviceDisconnected)
		return
	}
	info, known := r.devices[ski]
	if known && (len(info.RemoteEntities) > 0 || len(info.Entities) > 0) {
		r.setCapabilityLocked(ski, capability, CapabilityStateUnsupported, CapabilityReasonRemoteNotAdvertised)
		return
	}
	r.setCapabilityLocked(ski, capability, CapabilityStateTemporarilyUnavailable, CapabilityReasonEntityNotBound)
}
