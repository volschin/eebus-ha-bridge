package eebus

import (
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

func (r *DeviceRegistry) AddDevice(ski string, info DeviceInfo) {
	r.mu.Lock()
	info.SKI = ski
	r.devices[ski] = info
	r.mu.Unlock()
}

func (r *DeviceRegistry) RemoveDevice(ski string) {
	r.mu.Lock()
	delete(r.devices, ski)
	r.mu.Unlock()
}

func (r *DeviceRegistry) GetDevice(ski string) (DeviceInfo, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
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
	info, ok := r.devices[ski]
	if !ok || len(info.RemoteEntities) == 0 {
		return nil
	}
	return info.RemoteEntities[0]
}
