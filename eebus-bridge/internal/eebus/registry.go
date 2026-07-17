package eebus

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
)

type EntityInfo struct {
	Address  string
	Type     string
	Features []string
	Entity   spineapi.EntityRemoteInterface
}

type DeviceInfo struct {
	SKI            string
	Brand          string
	Model          string
	Serial         string
	DeviceType     string
	UseCases       []string
	RemoteDevice   spineapi.DeviceRemoteInterface
	RemoteEntities []spineapi.EntityRemoteInterface
	Entities       []EntityInfo
}

// EntityResolution describes a device-scoped entity lookup. DeviceCount is the
// number of distinct normalized device SKIs that matched an unscoped lookup.
// More than one match is ambiguous and deliberately has no selected Entity.
type EntityResolution struct {
	Entity      spineapi.EntityRemoteInterface
	DeviceCount int
}

func (r EntityResolution) Ambiguous() bool {
	return r.Entity == nil && r.DeviceCount > 1
}

type DeviceRegistry struct {
	mu                sync.RWMutex
	devices           map[string]DeviceInfo
	monitoring        map[string]deviceMonitoringState
	capabilities      map[string]map[Capability]DeviceCapability
	capabilitySupport map[string]map[Capability]map[string]bool
	localCapabilities map[Capability]bool
	clock             Clock
}

type deviceMonitoringState struct {
	connected                  bool
	connectedAt                time.Time
	lastTransitionAt           time.Time
	lastMonitoringSuccess      time.Time
	monitoringSuccessOnConnect bool
}

// Clock provides the current time for monitoring-health tracking. Tests can
// inject a deterministic implementation through NewDeviceRegistryWithClock.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time {
	return time.Now()
}

func NewDeviceRegistry() *DeviceRegistry {
	return NewDeviceRegistryWithClock(realClock{})
}

func NewDeviceRegistryWithClock(clock Clock) *DeviceRegistry {
	if clock == nil {
		clock = realClock{}
	}
	return &DeviceRegistry{
		devices:           make(map[string]DeviceInfo),
		monitoring:        make(map[string]deviceMonitoringState),
		capabilities:      make(map[string]map[Capability]DeviceCapability),
		capabilitySupport: make(map[string]map[Capability]map[string]bool),
		localCapabilities: make(map[Capability]bool),
		clock:             clock,
	}
}

// MarkConnected starts a fresh monitoring grace period for a device. The last
// success timestamp is retained for diagnostics, but cannot satisfy the new
// connection until RecordMonitoringSuccess is called again.
func (r *DeviceRegistry) MarkConnected(ski string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	ski = NormalizeSKI(ski)
	state := r.monitoring[ski]
	if state.connected {
		return
	}
	now := r.clock.Now()
	state.connected = true
	state.connectedAt = now
	state.lastTransitionAt = now
	state.monitoringSuccessOnConnect = false
	r.monitoring[ski] = state
	entries := r.ensureCapabilitiesLocked(ski)
	for capability, entry := range entries {
		if entry.Reason == CapabilityReasonDeviceDisconnected {
			r.setCapabilityLocked(ski, capability, CapabilityStateUnknown, CapabilityReasonEntityNotBound)
		}
	}
}

// MarkDisconnected excludes a device from monitoring-health checks. Unknown
// devices are ignored so a stray disconnect callback does not create state.
func (r *DeviceRegistry) MarkDisconnected(ski string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	ski = NormalizeSKI(ski)
	state, ok := r.monitoring[ski]
	if !ok || !state.connected {
		return
	}
	state.connected = false
	state.lastTransitionAt = r.clock.Now()
	r.monitoring[ski] = state
	for capability, entry := range r.ensureCapabilitiesLocked(ski) {
		if entry.State != CapabilityStateUnsupported {
			r.setCapabilityLocked(ski, capability, CapabilityStateTemporarilyUnavailable, CapabilityReasonDeviceDisconnected)
		}
	}
}

// DeviceConnection returns the current connection state and the time it last
// changed. A device with no monitoring state is unknown and reads as
// disconnected.
func (r *DeviceRegistry) DeviceConnection(ski string) (connected bool, lastTransition time.Time, known bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	state, ok := r.monitoring[NormalizeSKI(ski)]
	if !ok {
		return false, time.Time{}, false
	}
	return state.connected, state.lastTransitionAt, true
}

// RecordMonitoringSuccess marks that a remote entity was just successfully
// resolved for a live monitoring read. Call only on a real eebus-go scenario
// match (not a registry cache hit), so a stuck SPINE entity binding after
// reconnect is actually detected instead of masked by stale cached entities.
func (r *DeviceRegistry) RecordMonitoringSuccess(ski string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	ski = NormalizeSKI(ski)
	state := r.monitoring[ski]
	state.lastMonitoringSuccess = r.clock.Now()
	state.monitoringSuccessOnConnect = state.connected
	r.monitoring[ski] = state
}

// StaleDevices returns connected device SKIs whose grace period has elapsed
// without a success on the current connection, or whose most recent success
// on that connection is older than threshold.
func (r *DeviceRegistry) StaleDevices(threshold, gracePeriod time.Duration) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	now := r.clock.Now()
	result := make([]string, 0)
	for ski, state := range r.monitoring {
		if !state.connected || now.Sub(state.connectedAt) <= gracePeriod {
			continue
		}
		if !state.monitoringSuccessOnConnect || now.Sub(state.lastMonitoringSuccess) > threshold {
			result = append(result, ski)
		}
	}
	sort.Strings(result)
	return result
}

// MonitoringLastSuccessAge returns the age of a device's latest monitoring
// success, including one from a previous connection for watchdog diagnostics.
func (r *DeviceRegistry) MonitoringLastSuccessAge(ski string) (time.Duration, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	state, ok := r.monitoring[NormalizeSKI(ski)]
	if !ok || state.lastMonitoringSuccess.IsZero() {
		return 0, false
	}
	age := r.clock.Now().Sub(state.lastMonitoringSuccess)
	if age < 0 {
		age = 0
	}
	return age, true
}

// NormalizeSKI canonicalizes a SKI for use as a registry key: uppercase, with
// whitespace and common display separators removed. Remote peers and clients
// may report the same SKI with differing case or formatting; normalizing
// prevents one physical device from being stored under multiple keys.
func NormalizeSKI(ski string) string {
	replacer := strings.NewReplacer(" ", "", "\t", "", "\n", "", "\r", "", ":", "", "-", "")
	return strings.ToUpper(replacer.Replace(strings.TrimSpace(ski)))
}

// ShortSKI returns a normalized, redacted SKI suitable for log messages.
func ShortSKI(ski string) string {
	ski = NormalizeSKI(ski)
	if len(ski) <= 6 {
		return "…" + ski
	}
	return "…" + ski[len(ski)-6:]
}

func (r *DeviceRegistry) AddDevice(ski string, info DeviceInfo) {
	r.mu.Lock()
	ski = NormalizeSKI(ski)
	info.SKI = ski
	r.devices[ski] = info
	r.ensureCapabilitiesLocked(ski)
	r.mu.Unlock()
}

func (r *DeviceRegistry) UpsertObservation(
	ski string,
	remoteDevice spineapi.DeviceRemoteInterface,
	remoteEntity spineapi.EntityRemoteInterface,
	useCase string,
) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// A remote (e.g. Bosch/Connect-Key) can report the entity through the
	// use-case callback with an empty SKI. Fall back to the real remote device
	// SKI so HA's later WriteConsumptionLimit resolves the entity instead of
	// failing with NOT_FOUND.
	if ski == "" && remoteDevice != nil {
		ski = remoteDevice.Ski()
	}
	ski = NormalizeSKI(ski)
	r.ensureCapabilitiesLocked(ski)

	info := r.devices[ski]
	info.SKI = ski

	if remoteDevice != nil {
		info.RemoteDevice = remoteDevice
	}

	if remoteEntity != nil {
		alreadyPresent := false
		for _, existing := range info.RemoteEntities {
			if existing == remoteEntity {
				alreadyPresent = true
				break
			}
		}
		if !alreadyPresent {
			info.RemoteEntities = append(info.RemoteEntities, remoteEntity)
		}
		info.Entities = upsertEntityInfo(info.Entities, remoteEntity)
	}

	if useCase != "" {
		alreadyPresent := false
		for _, existing := range info.UseCases {
			if existing == useCase {
				alreadyPresent = true
				break
			}
		}
		if !alreadyPresent {
			info.UseCases = append(info.UseCases, useCase)
		}
	}

	r.devices[ski] = info
}

// UpsertDeviceClassification stores manufacturer/device-type metadata reported by
// a remote device. Empty values are ignored so later partial updates never clear
// previously discovered fields.
func (r *DeviceRegistry) UpsertDeviceClassification(ski, brand, deviceModel, serial, deviceType string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	ski = NormalizeSKI(ski)
	info := r.devices[ski]
	info.SKI = ski
	if brand != "" {
		info.Brand = brand
	}
	if deviceModel != "" {
		info.Model = deviceModel
	}
	if serial != "" {
		info.Serial = serial
	}
	if deviceType != "" {
		info.DeviceType = deviceType
	}
	r.devices[ski] = info
}

func (r *DeviceRegistry) RemoveDevice(ski string) {
	r.mu.Lock()
	ski = NormalizeSKI(ski)
	delete(r.devices, ski)
	delete(r.monitoring, ski)
	delete(r.capabilities, ski)
	delete(r.capabilitySupport, ski)
	r.mu.Unlock()
}

// ClearEntities drops the cached remote-device and remote-entity references for a
// SKI on disconnect while keeping the discovered classification metadata
// (brand/model/serial/type) and use-case list. Without this the registry would
// keep serving stale EntityRemoteInterface pointers after a SHIP/SPINE
// reconnect, so a subsequent OHPCF/LPC write would target an orphaned entity
// instead of the one re-negotiated on re-pair. A later UseCaseEvent re-populates
// the entities from fresh observations (cf. evcc-io/evcc#29628). No-op when the
// SKI is unknown.
func (r *DeviceRegistry) ClearEntities(ski string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ski = NormalizeSKI(ski)
	info, ok := r.devices[ski]
	if !ok {
		return
	}
	info.RemoteDevice = nil
	info.RemoteEntities = nil
	info.Entities = nil
	r.devices[ski] = info
}

// RemoveEntityObservation drops one entity removed from an upstream use-case
// scenario set without disturbing other entities exposed by the same gateway.
func (r *DeviceRegistry) RemoveEntityObservation(ski string, removed spineapi.EntityRemoteInterface) {
	if removed == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	ski = NormalizeSKI(ski)
	info, ok := r.devices[ski]
	if !ok {
		return
	}
	remoteEntities := info.RemoteEntities[:0]
	for _, entity := range info.RemoteEntities {
		if entity != removed {
			remoteEntities = append(remoteEntities, entity)
		}
	}
	info.RemoteEntities = remoteEntities
	removedAddress := EntityAddressString(removed.Address())
	entities := info.Entities[:0]
	for _, entity := range info.Entities {
		if entity.Entity != removed && (removedAddress == "" || entity.Address != removedAddress) {
			entities = append(entities, entity)
		}
	}
	info.Entities = entities
	r.devices[ski] = info
}

func (r *DeviceRegistry) GetDevice(ski string) (DeviceInfo, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ski = NormalizeSKI(ski)
	info, ok := r.devices[ski]
	return info, ok
}

func (r *DeviceRegistry) KnownDevice(ski string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ski = NormalizeSKI(ski)
	_, hasDevice := r.devices[ski]
	_, hasConnection := r.monitoring[ski]
	return hasDevice || hasConnection
}

func (r *DeviceRegistry) ListDevices() []DeviceInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]DeviceInfo, 0, len(r.devices))
	for _, info := range r.devices {
		result = append(result, info)
	}
	sort.Slice(result, func(i, j int) bool {
		return NormalizeSKI(result[i].SKI) < NormalizeSKI(result[j].SKI)
	})
	return result
}

func (r *DeviceRegistry) FirstEntity(ski string) spineapi.EntityRemoteInterface {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ski = NormalizeSKI(ski)
	info, ok := r.devices[ski]
	if !ok || len(info.RemoteEntities) == 0 {
		return nil
	}
	return info.RemoteEntities[0]
}

// FirstAvailableEntity resolves an entity only when exactly one known device
// currently has entities. Multiple devices are reported as ambiguous instead
// of depending on randomized map iteration order.
func (r *DeviceRegistry) FirstAvailableEntity() EntityResolution {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var entity spineapi.EntityRemoteInterface
	deviceCount := 0
	for _, info := range r.devices {
		if len(info.RemoteEntities) > 0 {
			deviceCount++
			if entity == nil {
				entity = info.RemoteEntities[0]
			}
		}
	}
	if deviceCount != 1 {
		entity = nil
	}
	return EntityResolution{Entity: entity, DeviceCount: deviceCount}
}

func (r *DeviceRegistry) Entities(ski string) []EntityInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ski = NormalizeSKI(ski)
	info, ok := r.devices[ski]
	if !ok {
		return nil
	}
	return copyEntityInfos(info.Entities)
}

func (r *DeviceRegistry) FirstEntityForType(ski, entityType string) spineapi.EntityRemoteInterface {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ski = NormalizeSKI(ski)
	info, ok := r.devices[ski]
	if !ok {
		return nil
	}
	for _, entity := range info.Entities {
		if entity.Type == entityType && entity.Entity != nil {
			return entity.Entity
		}
	}
	return nil
}

func upsertEntityInfo(entities []EntityInfo, entity spineapi.EntityRemoteInterface) []EntityInfo {
	next := EntityInfo{
		Address:  EntityAddressString(entity.Address()),
		Type:     string(entity.EntityType()),
		Features: FeatureStrings(entity.Features()),
		Entity:   entity,
	}
	for idx, existing := range entities {
		if existing.Entity == entity || (existing.Address != "" && existing.Address == next.Address) {
			entities[idx] = next
			return entities
		}
	}
	return append(entities, next)
}

func copyEntityInfos(in []EntityInfo) []EntityInfo {
	out := make([]EntityInfo, len(in))
	for idx, entity := range in {
		out[idx] = entity
		out[idx].Features = append([]string(nil), entity.Features...)
	}
	return out
}

func EntityAddressString(addr *model.EntityAddressType) string {
	if addr == nil || len(addr.Entity) == 0 {
		return ""
	}
	parts := make([]string, 0, len(addr.Entity))
	for _, a := range addr.Entity {
		parts = append(parts, fmt.Sprintf("%d", uint(a)))
	}
	return strings.Join(parts, ":")
}

func FeatureStrings(features []spineapi.FeatureRemoteInterface) []string {
	result := make([]string, 0, len(features))
	seen := make(map[string]struct{}, len(features))
	for _, feature := range features {
		if feature == nil {
			continue
		}
		key := fmt.Sprintf("%s/%s", feature.Type(), feature.Role())
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, key)
	}
	return result
}
