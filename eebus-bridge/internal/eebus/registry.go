package eebus

import (
	"strings"
	"sync"

	spineapi "github.com/enbility/spine-go/api"
)

type DeviceInfo struct {
	SKI            string
	Brand          string
	Model          string
	Serial         string
	DeviceType     string
	UseCases       []string
	RemoteDevice   spineapi.DeviceRemoteInterface
	RemoteEntities []spineapi.EntityRemoteInterface
}

type DeviceRegistry struct {
	mu      sync.RWMutex
	devices map[string]DeviceInfo
}

func NewDeviceRegistry() *DeviceRegistry {
	return &DeviceRegistry{
		devices: make(map[string]DeviceInfo),
	}
}

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

	// Bosch/Connect-Key can report the remote entity via the use-case callback
	// while the callback SKI is empty. In that case store the entity under the
	// real remote device SKI; otherwise HA later calls WriteConsumptionLimit with
	// the Bosch SKI and resolveEntity() fails with NOT_FOUND.
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

func (r *DeviceRegistry) RemoveDevice(ski string) {
	r.mu.Lock()
	ski = NormalizeSKI(ski)
	delete(r.devices, ski)
	r.mu.Unlock()
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
