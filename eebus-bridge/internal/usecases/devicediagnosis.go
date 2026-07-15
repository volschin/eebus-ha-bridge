package usecases

import (
	"errors"
	"log"
	"time"

	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

var ErrDeviceOperatingStateUnavailable = errors.New("device operating state unavailable")

var deviceOperatingStateReadTimeout = 5 * time.Second

// DeviceOperatingState registers its own DeviceDiagnosis/client feature and
// actively reads the remote DeviceDiagnosis/server feature because no existing
// EEBUS use case negotiates or refreshes that feature.
type DeviceOperatingState struct {
	bus         *eebus.EventBus
	registry    *eebus.DeviceRegistry
	localEntity spineapi.EntityLocalInterface
	debug       bool
}

func NewDeviceOperatingState(
	bus *eebus.EventBus,
	registry *eebus.DeviceRegistry,
	debug bool,
) *DeviceOperatingState {
	return &DeviceOperatingState{bus: bus, registry: registry, debug: debug}
}

// Setup registers the local DeviceDiagnosis client before service startup and
// subscribes to raw cache updates from the local device.
func (d *DeviceOperatingState) Setup(localEntity spineapi.EntityLocalInterface) {
	if localEntity == nil {
		return
	}
	d.localEntity = localEntity
	localEntity.GetOrAddFeature(model.FeatureTypeTypeDeviceDiagnosis, model.RoleTypeClient)
	_ = localEntity.Device().Events().Subscribe(d)
}

// HandleEvent republishes valid DeviceDiagnosis cache updates as typed events.
// The payload already carries the fresh state; reading it here instead of
// issuing another RequestRemoteData avoids a reply→event→read feedback loop.
func (d *DeviceOperatingState) HandleEvent(payload spineapi.EventPayload) {
	if payload.Entity == nil || payload.EventType != spineapi.EventTypeDataChange ||
		payload.ChangeType != spineapi.ElementChangeUpdate {
		return
	}
	if _, ok := deviceOperatingState(payload.Data); !ok {
		return
	}
	if d.bus == nil {
		return
	}
	ski := eebus.NormalizeSKI(payload.Ski)
	d.bus.Publish(eebus.Event{SKI: ski, Type: eebus.EventTypeMonitoringDeviceOperatingStateUpdated})
}

// OperatingState actively reads and returns the remote device operating state.
func (d *DeviceOperatingState) OperatingState(ski string) (string, error) {
	local := d.localDeviceDiagnosisFeature()
	remote := d.remoteDeviceDiagnosisFeature(ski)
	if local == nil || remote == nil {
		return "", ErrDeviceOperatingStateUnavailable
	}

	counter, requestErr := local.RequestRemoteData(
		model.FunctionTypeDeviceDiagnosisStateData,
		nil,
		nil,
		remote,
	)
	if requestErr != nil {
		if d.debug {
			log.Printf("[DeviceDiagnosis] requesting operating state failed: %s", requestErr.String())
		}
		return operatingStateFromCache(remote)
	}
	if counter == nil {
		return operatingStateFromCache(remote)
	}

	response := make(chan any, 1)
	if err := local.AddResponseCallback(*counter, func(message spineapi.ResponseMessage) {
		select {
		case response <- message.Data:
		default:
		}
	}); err != nil {
		if d.debug {
			log.Printf("[DeviceDiagnosis] registering operating-state response callback failed: %v", err)
		}
		return operatingStateFromCache(remote)
	}

	timer := time.NewTimer(deviceOperatingStateReadTimeout)
	defer timer.Stop()
	select {
	case data := <-response:
		if state, ok := deviceOperatingState(data); ok {
			return state, nil
		}
		return operatingStateFromCache(remote)
	case <-timer.C:
		return operatingStateFromCache(remote)
	}
}

// CachedOperatingState returns the operating state from the SPINE cache
// without touching the network. Suitable for event fan-out, where the cache
// was updated immediately before the event fired.
func (d *DeviceOperatingState) CachedOperatingState(ski string) (string, error) {
	remote := d.remoteDeviceDiagnosisFeature(ski)
	if remote == nil {
		return "", ErrDeviceOperatingStateUnavailable
	}
	return operatingStateFromCache(remote)
}

func (d *DeviceOperatingState) localDeviceDiagnosisFeature() spineapi.FeatureLocalInterface {
	if d.localEntity == nil {
		return nil
	}
	return d.localEntity.FeatureOfTypeAndRole(model.FeatureTypeTypeDeviceDiagnosis, model.RoleTypeClient)
}

func (d *DeviceOperatingState) remoteDeviceDiagnosisFeature(ski string) spineapi.FeatureRemoteInterface {
	if d.registry == nil {
		return nil
	}
	var fallback spineapi.FeatureRemoteInterface
	for _, info := range d.registry.Entities(ski) {
		if info.Entity == nil {
			continue
		}
		feature := info.Entity.FeatureOfTypeAndRole(model.FeatureTypeTypeDeviceDiagnosis, model.RoleTypeServer)
		if feature == nil {
			continue
		}
		if info.Type == string(model.EntityTypeTypeHeatPumpAppliance) {
			return feature
		}
		if fallback == nil {
			fallback = feature
		}
	}
	return fallback
}

func operatingStateFromCache(feature spineapi.FeatureRemoteInterface) (string, error) {
	if state, ok := deviceOperatingState(feature.DataCopy(model.FunctionTypeDeviceDiagnosisStateData)); ok {
		return state, nil
	}
	return "", ErrDeviceOperatingStateUnavailable
}

func deviceOperatingState(data any) (string, bool) {
	state, ok := data.(*model.DeviceDiagnosisStateDataType)
	if !ok || state == nil || state.OperatingState == nil {
		return "", false
	}
	return string(*state.OperatingState), true
}
