package usecases

import (
	"errors"
	"fmt"
	"log"
	"time"

	eebusapi "github.com/enbility/eebus-go/api"
	ucapi "github.com/enbility/eebus-go/usecases/api"
	cemohpcf "github.com/enbility/eebus-go/usecases/cem/ohpcf"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

// OHPCFWrapper wraps the eebus-go OHPCF (Optimization of Self-Consumption by Heat
// Pump Compressor Flexibility) use case and routes events to the internal
// EventBus. The bridge plays the CEM (controller/client) actor; the remote heat
// pump's Compressor entity is the flexibility provider.
//
// This is the CEM-client side of §1.3.1 PV-surplus compressor control: it reads
// the remote offer/state and drives schedule/pause/resume/abort via OHPCFService
// (see docs/eebus-vaillant-improvements.md).
type OHPCFWrapper struct {
	uc          *cemohpcf.OHPCF
	bus         *eebus.EventBus
	registry    *eebus.DeviceRegistry
	localEntity spineapi.EntityLocalInterface
	debug       bool
}

var errOHPCFNotInitialized = errors.New("ohpcf use case not initialized")

var ErrOHPCFRejected = errors.New("OHPCF control rejected by device")

// NewOHPCFWrapper creates a new OHPCFWrapper. Call Setup() before using the use case.
func NewOHPCFWrapper(bus *eebus.EventBus, registry *eebus.DeviceRegistry, debugEvents bool) *OHPCFWrapper {
	return &OHPCFWrapper{bus: bus, registry: registry, debug: debugEvents}
}

// Setup initialises the underlying eebus-go OHPCF use case for the given local
// entity. It is idempotent: a second call is a no-op, so the use case and its
// explicit event-bus subscription are registered exactly once. Like the LPC and
// monitoring wrappers, the registration lives for the process lifetime and is not
// torn down (the bridge stops by exiting), so no unsubscribe path is provided.
func (w *OHPCFWrapper) Setup(localEntity spineapi.EntityLocalInterface) {
	if localEntity == nil || w.uc != nil {
		return
	}
	w.localEntity = localEntity
	w.uc = cemohpcf.NewOHPCF(localEntity, w.HandleEvent)

	// Workaround for enbility/eebus-go#228: unlike NewMPC / NewLPC, NewOHPCF does
	// not subscribe its concrete use case to the device event bus. Without this,
	// OHPCF.HandleEvent never runs, so its connected() Subscribe()/Bind() to the
	// remote SmartEnergyManagementPs feature is never issued and the SHIP trace
	// shows no binding. Register it explicitly until #228 lands upstream.
	_ = localEntity.Device().Events().Subscribe(w.uc)
}

// UseCase returns the underlying eebus-go OHPCF use case (may be nil before Setup).
func (w *OHPCFWrapper) UseCase() *cemohpcf.OHPCF {
	return w.uc
}

// HandleEvent is the api.EntityEventCallback passed to eebus-go. It translates
// eebus-go OHPCF event types to internal EventBus events.
func (w *OHPCFWrapper) HandleEvent(ski string, device spineapi.DeviceRemoteInterface, entity spineapi.EntityRemoteInterface, event eebusapi.EventType) {
	if w.debug {
		log.Printf(
			"[DEBUG] EEBUS ohpcf event received: ski=%s event=%s has_device=%t has_entity=%t",
			ski,
			event,
			device != nil,
			entity != nil,
		)
		eebus.DefaultUseCaseDiscovery().LogOnce(ski, device)
	}

	isSupportUpdate := event == cemohpcf.UseCaseSupportUpdate
	if w.registry != nil && !isSupportUpdate {
		w.registry.UpsertObservation(ski, device, entity, "ohpcf")
		enrichDeviceClassification(w.registry, w.localEntity, ski, device, entity)
	}

	var eventType eebus.EventType
	switch event {
	case cemohpcf.UseCaseSupportUpdate:
		eventType = eebus.EventTypeOHPCFUseCaseSupportUpdated
		resolution := w.CompatibleEntity(observationSKI(ski, device))
		if recordCapabilitySupport(w.registry, ski, device, entity, resolution, "ohpcf", eebus.CapabilityOHPCF) {
			enrichDeviceClassification(w.registry, w.localEntity, ski, device, resolution.Entity)
		}
	case cemohpcf.DataUpdateConsumptionState:
		eventType = eebus.EventTypeOHPCFConsumptionStateUpdated
	case cemohpcf.DataUpdateConsumptionIsStoppable:
		eventType = eebus.EventTypeOHPCFConsumptionStoppableUpdated
	case cemohpcf.DataUpdateConsumptionIsPausable:
		eventType = eebus.EventTypeOHPCFConsumptionPausableUpdated
	case cemohpcf.DataUpdateConsumptionStartTime:
		eventType = eebus.EventTypeOHPCFConsumptionStartTimeUpdated
	case cemohpcf.DataUpdateRequestedPowerEstimate:
		eventType = eebus.EventTypeOHPCFRequestedPowerEstimateUpdated
	case cemohpcf.DataUpdateRequestedPowerMax:
		eventType = eebus.EventTypeOHPCFRequestedPowerMaxUpdated
	case cemohpcf.DataUpdateMinimalRunDuration:
		eventType = eebus.EventTypeOHPCFMinimalRunDurationUpdated
	case cemohpcf.DataUpdateMinimalPauseDuration:
		eventType = eebus.EventTypeOHPCFMinimalPauseDurationUpdated
	default:
		return
	}
	if w.bus != nil {
		w.bus.Publish(eebus.Event{SKI: ski, Type: eventType})
	}
}

// OptionalPowerConsumptionAvailable reports whether the remote compressor entity
// currently advertises an optional power-consumption process the CEM may schedule.
func (w *OHPCFWrapper) OptionalPowerConsumptionAvailable(entity spineapi.EntityRemoteInterface) (bool, error) {
	if w.uc == nil {
		return false, errOHPCFNotInitialized
	}
	return w.uc.OptionalPowerConsumptionAvailable(entity)
}

// CompatibleEntity returns the first remote entity that actually supports the
// OHPCF use case (the heat pump's Compressor entity) for the given SKI. A gateway
// such as the Vaillant VR940 registers several entities under one device SKI;
// RemoteEntitiesScenarios lists only entities that advertise OHPCF, so resolving
// from it picks the Compressor rather than e.g. the monitoring meter (issue #47).
// An empty ski resolves only when exactly one device is OHPCF-capable.
func (w *OHPCFWrapper) CompatibleEntity(ski string) eebus.EntityResolution {
	if w.uc == nil {
		return eebus.EntityResolution{}
	}
	return compatibleEntity(w.uc.RemoteEntitiesScenarios(), ski)
}

// RequestedPowerEstimate returns the estimated power (W) of the offered optional
// consumption.
func (w *OHPCFWrapper) RequestedPowerEstimate(entity spineapi.EntityRemoteInterface) (float64, error) {
	if w.uc == nil {
		return 0, errOHPCFNotInitialized
	}
	return w.uc.RequestedPowerEstimate(entity)
}

// RequestedPowerMax returns the maximum power (W) of the offered optional consumption.
func (w *OHPCFWrapper) RequestedPowerMax(entity spineapi.EntityRemoteInterface) (float64, error) {
	if w.uc == nil {
		return 0, errOHPCFNotInitialized
	}
	return w.uc.RequestedPowerMax(entity)
}

// ConsumptionIsStoppable reports whether the running process may be aborted.
func (w *OHPCFWrapper) ConsumptionIsStoppable(entity spineapi.EntityRemoteInterface) (bool, error) {
	if w.uc == nil {
		return false, errOHPCFNotInitialized
	}
	return w.uc.ConsumptionIsStoppable(entity)
}

// ConsumptionIsPausable reports whether the running process may be paused.
func (w *OHPCFWrapper) ConsumptionIsPausable(entity spineapi.EntityRemoteInterface) (bool, error) {
	if w.uc == nil {
		return false, errOHPCFNotInitialized
	}
	return w.uc.ConsumptionIsPausable(entity)
}

// ConsumptionState returns the current state of the optional consumption process.
func (w *OHPCFWrapper) ConsumptionState(entity spineapi.EntityRemoteInterface) (ucapi.CompressorPowerConsumptionStateType, error) {
	if w.uc == nil {
		return "", errOHPCFNotInitialized
	}
	return w.uc.PowerConsumptionProcessState(entity)
}

// ConsumptionStartTime returns the scheduled start of the optional process.
func (w *OHPCFWrapper) ConsumptionStartTime(entity spineapi.EntityRemoteInterface) (time.Time, error) {
	if w.uc == nil {
		return time.Time{}, errOHPCFNotInitialized
	}
	return w.uc.PowerConsumptionProcessStartTime(entity)
}

// MinimalRunDuration returns the minimum run duration the CEM must honour.
func (w *OHPCFWrapper) MinimalRunDuration(entity spineapi.EntityRemoteInterface) (time.Duration, error) {
	if w.uc == nil {
		return 0, errOHPCFNotInitialized
	}
	return w.uc.PowerConsumptionMinimalRunDuration(entity)
}

// MinimalPauseDuration returns the minimum pause duration the CEM must honour.
func (w *OHPCFWrapper) MinimalPauseDuration(entity spineapi.EntityRemoteInterface) (time.Duration, error) {
	if w.uc == nil {
		return 0, errOHPCFNotInitialized
	}
	return w.uc.PowerConsumptionMinimalPauseDuration(entity)
}

// ohpcfWriteTimeout bounds how long a control write waits for the heat pump's
// result before giving up. It is a var, not a const, so tests can shorten it.
var ohpcfWriteTimeout = 10 * time.Second

// awaitWrite issues an OHPCF control write and blocks until the remote heat pump
// returns its result, mapping a rejection or a missing result to an error. The
// write closure must invoke the supplied callback exactly once with the device's
// ResultDataType (eebus-go guarantees this for a successfully sent command).
//
// Without this the bridge would report success as soon as the command was *sent*,
// masking a device-side rejection (e.g. an uncommissioned heat pump) from the HA
// caller. Mirrors evcc's server/eebus.Await (evcc-io/evcc#31350).
func awaitWrite(action string, write func(func(model.ResultDataType, model.MsgCounterType)) (*model.MsgCounterType, error)) error {
	res := make(chan model.ResultDataType, 1)

	if _, err := write(func(r model.ResultDataType, _ model.MsgCounterType) { res <- r }); err != nil {
		return err
	}

	select {
	case r := <-res:
		if r.ErrorNumber != nil && uint(*r.ErrorNumber) != 0 {
			err := fmt.Errorf("%w: action=%s error=%d", ErrOHPCFRejected, action, *r.ErrorNumber)
			if r.Description != nil && *r.Description != "" {
				err = fmt.Errorf("%w (%s)", err, *r.Description)
			}
			log.Printf("[OHPCF] %v", err)
			return err
		}
		return nil
	case <-time.After(ohpcfWriteTimeout):
		return fmt.Errorf("ohpcf %s: timed out waiting for device result", action)
	}
}

// Schedule starts the optional power-consumption process, immediately when start
// is zero (or in the past) or after the given time otherwise. eebus-go's
// SchedulePowerConsumptionProcess takes a relative delay, not an absolute time, so
// convert here to keep the gRPC contract (absolute start time) unchanged.
func (w *OHPCFWrapper) Schedule(entity spineapi.EntityRemoteInterface, start time.Time) error {
	if w.uc == nil {
		return errOHPCFNotInitialized
	}
	var startIn time.Duration
	if !start.IsZero() {
		if d := time.Until(start); d > 0 {
			startIn = d
		}
	}
	return awaitWrite("schedule", func(cb func(model.ResultDataType, model.MsgCounterType)) (*model.MsgCounterType, error) {
		return w.uc.SchedulePowerConsumptionProcess(entity, startIn, cb)
	})
}

// Pause pauses the running optional power-consumption process.
func (w *OHPCFWrapper) Pause(entity spineapi.EntityRemoteInterface) error {
	if w.uc == nil {
		return errOHPCFNotInitialized
	}
	return awaitWrite("pause", func(cb func(model.ResultDataType, model.MsgCounterType)) (*model.MsgCounterType, error) {
		return w.uc.PausePowerConsumptionProcess(entity, cb)
	})
}

// Resume resumes a paused optional power-consumption process.
func (w *OHPCFWrapper) Resume(entity spineapi.EntityRemoteInterface) error {
	if w.uc == nil {
		return errOHPCFNotInitialized
	}
	return awaitWrite("resume", func(cb func(model.ResultDataType, model.MsgCounterType)) (*model.MsgCounterType, error) {
		return w.uc.ResumePowerConsumptionProcess(entity, cb)
	})
}

// Abort aborts the optional power-consumption process.
func (w *OHPCFWrapper) Abort(entity spineapi.EntityRemoteInterface) error {
	if w.uc == nil {
		return errOHPCFNotInitialized
	}
	return awaitWrite("abort", func(cb func(model.ResultDataType, model.MsgCounterType)) (*model.MsgCounterType, error) {
		return w.uc.AbortPowerConsumptionProcess(entity, cb)
	})
}
