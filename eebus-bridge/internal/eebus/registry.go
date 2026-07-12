package eebus

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
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

type DeviceRegistry struct {
	mu                    sync.RWMutex
	devices               map[string]DeviceInfo
	lastMonitoringSuccess atomic.Int64 // unix nano; set at construction so a stuck startup also counts as stale
}

func NewDeviceRegistry() *DeviceRegistry {
	r := &DeviceRegistry{
		devices: make(map[string]DeviceInfo),
	}
	r.lastMonitoringSuccess.Store(time.Now().UnixNano())
	return r
}

// RecordMonitoringSuccess marks that a remote entity was just successfully
// resolved for a live monitoring read. Call only on a real eebus-go scenario
// match (not a registry cache hit), so a stuck SPINE entity binding after
// reconnect is actually detected instead of masked by stale cached entities.
func (r *DeviceRegistry) RecordMonitoringSuccess() {
	r.lastMonitoringSuccess.Store(time.Now().UnixNano())
}

// MonitoringStale reports whether monitoring reads have produced no
// successful entity resolution for longer than threshold, while at least one
// device is trusted. Returns false with no trusted device, since a bridge
// that has never been paired isn't a monitoring outage.
func (r *DeviceRegistry) MonitoringStale(threshold time.Duration) bool {
	if len(r.ListDevices()) == 0 {
		return false
	}
	last := time.Unix(0, r.lastMonitoringSuccess.Load())
	return time.Since(last) > threshold
}

// NormalizeSKI canonicalizes a SKI for use as a registry key: uppercase, no
// surrounding or embedded whitespace. Remote peers (e.g. Bosch/Connect-Key) may
// report the same SKI with differing case or spacing; normalizing prevents the
// same device being stored under multiple keys, which later causes resolveEntity
// lookups to fail with NOT_FOUND.
func NormalizeSKI(ski string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(ski), " ", ""))
}

func (r *DeviceRegistry) AddDevice(ski string, info DeviceInfo) {
	r.mu.Lock()
	ski = NormalizeSKI(ski)
	info.SKI = ski
	r.devices[ski] = info
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

func (r *DeviceRegistry) GetDevice(ski string) (DeviceInfo, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ski = NormalizeSKI(ski)
	info, ok := r.devices[ski]
	return info, ok
}

func (r *DeviceRegistry) ListDevices() []DeviceInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]DeviceInfo, 0, len(r.devices))
	for _, info := range r.devices {
		result = append(result, info)
	}
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

// FirstAvailableEntity returns the first entity from any known device.
// Used as a fallback when a client-selected SKI has no mapped entity yet.
func (r *DeviceRegistry) FirstAvailableEntity() spineapi.EntityRemoteInterface {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, info := range r.devices {
		if len(info.RemoteEntities) > 0 {
			return info.RemoteEntities[0]
		}
	}
	return nil
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
