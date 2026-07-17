package usecases

import (
	"errors"
	"log"
	"time"

	eebusapi "github.com/enbility/eebus-go/api"
	ucapi "github.com/enbility/eebus-go/usecases/api"
	eglpc "github.com/enbility/eebus-go/usecases/eg/lpc"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

// LPCWrapper wraps the eebus-go LPC (Limitation of Power Consumption) use case
// and routes events to the internal EventBus.
type LPCWrapper struct {
	uc          *eglpc.LPC
	bus         *eebus.EventBus
	registry    *eebus.DeviceRegistry
	localEntity spineapi.EntityLocalInterface
	debug       bool
}

var errLPCNotInitialized = errors.New("lpc use case not initialized")

// NewLPCWrapper creates a new LPCWrapper. Call Setup() before using the use case.
func NewLPCWrapper(bus *eebus.EventBus, registry *eebus.DeviceRegistry, debugEvents bool) *LPCWrapper {
	return &LPCWrapper{bus: bus, registry: registry, debug: debugEvents}
}

// Setup initialises the underlying eebus-go LPC use case for the given local entity.
func (w *LPCWrapper) Setup(localEntity spineapi.EntityLocalInterface) {
	if localEntity == nil {
		return
	}
	w.localEntity = localEntity
	w.uc = eglpc.NewLPC(localEntity, w.HandleEvent)
}

// UseCase returns the underlying eebus-go LPC use case (may be nil before Setup).
func (w *LPCWrapper) UseCase() *eglpc.LPC {
	return w.uc
}

// HandleEvent is the api.EntityEventCallback passed to eebus-go. It translates
// eebus-go event types to internal EventBus events.
func (w *LPCWrapper) HandleEvent(ski string, device spineapi.DeviceRemoteInterface, entity spineapi.EntityRemoteInterface, event eebusapi.EventType) {
	if w.debug {
		log.Printf(
			"[DEBUG] EEBUS LPC event received: ski=%s event=%s has_device=%t has_entity=%t",
			ski,
			event,
			device != nil,
			entity != nil,
		)
	}

	if w.debug {
		eebus.DefaultUseCaseDiscovery().LogOnce(ski, device)
	}

	isSupportUpdate := event == eglpc.UseCaseSupportUpdate
	if w.registry != nil && !isSupportUpdate {
		w.registry.UpsertObservation(ski, device, entity, "lpc")
		enrichDeviceClassification(w.registry, w.localEntity, ski, device, entity)
	}

	var eventType eebus.EventType
	switch event {
	case eglpc.DataUpdateLimit:
		eventType = eebus.EventTypeLPCLimitUpdated
	case eglpc.DataUpdateFailsafeConsumptionActivePowerLimit:
		eventType = eebus.EventTypeLPCFailsafePowerUpdated
	case eglpc.DataUpdateFailsafeDurationMinimum:
		eventType = eebus.EventTypeLPCFailsafeDurationUpdated
	case eglpc.UseCaseSupportUpdate:
		eventType = eebus.EventTypeLPCUseCaseSupportUpdated
		var scenarios []eebusapi.RemoteEntityScenarios
		if w.uc != nil {
			scenarios = w.uc.RemoteEntitiesScenarios()
		}
		resolvedSKI := observationSKI(ski, device)
		overall := compatibleEntity(scenarios, resolvedSKI)
		if w.registry != nil {
			connected, _, connectionKnown := w.registry.DeviceConnection(resolvedSKI)
			if connectionKnown && !connected {
				w.registry.RemoveEntityObservation(resolvedSKI, entity)
			} else if entity != nil && !entityPresentInScenarios(scenarios, entity) {
				w.registry.RemoveEntityObservation(resolvedSKI, entity)
			}
			observedEntity := overall.Entity
			if entityPresentInScenarios(scenarios, entity) {
				observedEntity = entity
			}
			if observedEntity != nil && (!connectionKnown || connected) {
				w.registry.UpsertObservation(resolvedSKI, device, observedEntity, "lpc")
			}
		}
		for _, support := range []struct {
			capability eebus.Capability
			scenario   uint
		}{
			{eebus.CapabilityLPC, 1},
			{eebus.CapabilityFailsafe, 2},
			{eebus.CapabilityHeartbeat, 3},
		} {
			resolution := compatibleEntityForScenario(scenarios, resolvedSKI, support.scenario)
			if w.registry != nil {
				w.registry.RecordCapabilitySupport(resolvedSKI, support.capability, resolution.Entity != nil)
			}
			if w.registry != nil && resolution.Entity != nil {
				enrichDeviceClassification(w.registry, w.localEntity, ski, device, resolution.Entity)
			}
		}
	case eglpc.DataUpdateHeartbeat:
		// Per eebus-go: signals the remote entering or leaving failsafe state.
		// No payload is attached; HA reconciles via GetHeartbeatStatus on refresh.
		eventType = eebus.EventTypeLPCHeartbeatUpdated
	default:
		return
	}
	if w.bus != nil {
		w.bus.Publish(eebus.Event{SKI: ski, Type: eventType})
	}
}

// CompatibleEntity returns the first remote entity that actually supports the LPC
// (Limitation of Power Consumption) use case for the given SKI. A gateway can
// register several entities under one device SKI — e.g. a Vaillant VR940f exposes
// both an EG-M monitoring meter entity and the heat-pump LPC entity. The flat
// device registry returns whichever entity was observed first, so an LPC write may
// be handed the monitoring entity, which eebus-go rejects with ErrNoCompatibleEntity
// ("no compatible entity"). RemoteEntitiesScenarios lists only entities that
// advertise the LPC use case, so resolving from it picks the correct one (issue #47).
//
// An empty ski resolves only when exactly one device is LPC-capable.
func (w *LPCWrapper) CompatibleEntity(ski string) eebus.EntityResolution {
	if w.uc == nil {
		return eebus.EntityResolution{}
	}
	return compatibleEntity(w.uc.RemoteEntitiesScenarios(), ski)
}

func (w *LPCWrapper) CompatibleEntityForScenario(ski string, scenario uint) eebus.EntityResolution {
	if w.uc == nil {
		return eebus.EntityResolution{}
	}
	return compatibleEntityForScenario(w.uc.RemoteEntitiesScenarios(), ski, scenario)
}

// ConsumptionLimit returns the current load control limit for the given remote entity.
func (w *LPCWrapper) ConsumptionLimit(entity spineapi.EntityRemoteInterface) (ucapi.LoadLimit, error) {
	if w.uc == nil {
		return ucapi.LoadLimit{}, errLPCNotInitialized
	}
	return w.uc.ConsumptionLimit(entity)
}

// WriteConsumptionLimit sends a new consumption limit to the given remote entity.
func (w *LPCWrapper) WriteConsumptionLimit(entity spineapi.EntityRemoteInterface, limit ucapi.LoadLimit) error {
	if w.uc == nil {
		return errLPCNotInitialized
	}
	_, err := w.uc.WriteConsumptionLimit(entity, limit, nil)
	return err
}

// FailsafeConsumptionActivePowerLimit returns the failsafe active power limit.
func (w *LPCWrapper) FailsafeConsumptionActivePowerLimit(entity spineapi.EntityRemoteInterface) (float64, error) {
	if w.uc == nil {
		return 0, errLPCNotInitialized
	}
	return w.uc.FailsafeConsumptionActivePowerLimit(entity)
}

// WriteFailsafeConsumptionActivePowerLimit sends a new failsafe active power limit.
func (w *LPCWrapper) WriteFailsafeConsumptionActivePowerLimit(entity spineapi.EntityRemoteInterface, value float64) error {
	if w.uc == nil {
		return errLPCNotInitialized
	}
	_, err := w.uc.WriteFailsafeConsumptionActivePowerLimit(entity, value)
	return err
}

// FailsafeDurationMinimum returns the minimum failsafe duration.
func (w *LPCWrapper) FailsafeDurationMinimum(entity spineapi.EntityRemoteInterface) (time.Duration, error) {
	if w.uc == nil {
		return 0, errLPCNotInitialized
	}
	return w.uc.FailsafeDurationMinimum(entity)
}

// WriteFailsafeDurationMinimum sends a new minimum failsafe duration (must be 2h–24h).
func (w *LPCWrapper) WriteFailsafeDurationMinimum(entity spineapi.EntityRemoteInterface, duration time.Duration) error {
	if w.uc == nil {
		return errLPCNotInitialized
	}
	_, err := w.uc.WriteFailsafeDurationMinimum(entity, duration)
	return err
}

// StartHeartbeat starts the local entity's periodic DeviceDiagnosis heartbeat.
// Controllable systems (e.g. heat pumps) drop an LPC limit to its failsafe value
// when heartbeats stop arriving, so the bridge must keep the heartbeat running.
// The ski argument is accepted for API symmetry but unused: the heartbeat is a
// property of the local entity, not of a specific remote.
func (w *LPCWrapper) StartHeartbeat(_ string) error {
	if w.localEntity == nil {
		return errLPCNotInitialized
	}
	return w.localEntity.HeartbeatManager().StartHeartbeat()
}

// StopHeartbeat stops the periodic heartbeat requests.
func (w *LPCWrapper) StopHeartbeat() error {
	if w.localEntity == nil {
		return errLPCNotInitialized
	}
	w.localEntity.HeartbeatManager().StopHeartbeat()
	return nil
}

// IsHeartbeatRunning reports whether the local heartbeat is currently active.
func (w *LPCWrapper) IsHeartbeatRunning() bool {
	if w.localEntity == nil {
		return false
	}
	return w.localEntity.HeartbeatManager().IsHeartbeatRunning()
}

// IsHeartbeatWithinDuration reports whether entity (the remote controllable
// system) has sent a DeviceDiagnosis heartbeat within the last 2 minutes.
// This is distinct from IsHeartbeatRunning: that reports whether *we* are
// still sending our own heartbeat (nearly always true), whereas this is the
// actual §14a failsafe signal — a heat pump drops its LPC limit to the
// configured failsafe value once its own heartbeat to us lapses, and this
// method is how the bridge detects that has happened.
func (w *LPCWrapper) IsHeartbeatWithinDuration(entity spineapi.EntityRemoteInterface) bool {
	if w.uc == nil {
		return false
	}
	return w.uc.IsHeartbeatWithinDuration(entity)
}
