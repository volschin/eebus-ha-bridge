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
	// No-op unless the experimental HVAC probe was armed in main.
	eebus.DefaultHvacProbe().ProbeOnce(ski, device)

	if w.registry != nil {
		w.registry.UpsertObservation(ski, device, entity, "lpc")
		enrichDeviceClassification(w.registry, w.localEntity, ski, device, entity)
	}

	var eventType string
	switch event {
	case eglpc.DataUpdateLimit:
		eventType = "lpc.limit_updated"
	case eglpc.DataUpdateFailsafeConsumptionActivePowerLimit:
		eventType = "lpc.failsafe_power_updated"
	case eglpc.DataUpdateFailsafeDurationMinimum:
		eventType = "lpc.failsafe_duration_updated"
	case eglpc.UseCaseSupportUpdate:
		eventType = "lpc.use_case_support_updated"
	case eglpc.DataUpdateHeartbeat:
		// Per eebus-go: signals the remote entering or leaving failsafe state.
		// No payload is attached; HA reconciles via GetHeartbeatStatus on refresh.
		eventType = "lpc.heartbeat_updated"
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
// An empty ski matches the first LPC-capable entity of any device. Returns nil when
// the use case is not set up or no compatible entity has been negotiated yet.
func (w *LPCWrapper) CompatibleEntity(ski string) spineapi.EntityRemoteInterface {
	if w.uc == nil {
		return nil
	}
	return compatibleEntity(w.uc.RemoteEntitiesScenarios(), ski)
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
