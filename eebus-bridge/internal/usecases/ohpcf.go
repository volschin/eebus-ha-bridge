package usecases

import (
	"errors"
	"log"

	eebusapi "github.com/enbility/eebus-go/api"
	cemohpcf "github.com/enbility/eebus-go/usecases/cem/ohpcf"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/volschin/eebus-bridge/internal/eebus"
)

// OHPCFWrapper wraps the eebus-go OHPCF (Optimization of Self-Consumption by Heat
// Pump Compressor Flexibility) use case and routes events to the internal
// EventBus. The bridge plays the CEM (controller/client) actor; the remote heat
// pump's Compressor entity is the flexibility provider.
//
// SPIKE: this is the CEM-client side of §1.3.1 PV-surplus compressor control.
// Currently a read-only observer used to confirm whether the VR940 actually binds
// and serves its SmartEnergyManagementPs feature. The schedule/pause/resume/abort
// control path is intentionally not wired yet (see docs/eebus-vaillant-improvements.md).
type OHPCFWrapper struct {
	uc          *cemohpcf.OHPCF
	bus         *eebus.EventBus
	registry    *eebus.DeviceRegistry
	localEntity spineapi.EntityLocalInterface
	debug       bool
}

var errOHPCFNotInitialized = errors.New("ohpcf use case not initialized")

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

	if w.registry != nil {
		w.registry.UpsertObservation(ski, device, entity, "ohpcf")
		enrichDeviceClassification(w.registry, w.localEntity, ski, device, entity)
	}

	var eventType string
	switch event {
	case cemohpcf.UseCaseSupportUpdate:
		eventType = "ohpcf.use_case_support_updated"
	case cemohpcf.DataUpdateConsumptionState:
		eventType = "ohpcf.consumption_state_updated"
	case cemohpcf.DataUpdateConsumptionIsStoppable:
		eventType = "ohpcf.consumption_stoppable_updated"
	case cemohpcf.DataUpdateConsumptionIsPausable:
		eventType = "ohpcf.consumption_pausable_updated"
	case cemohpcf.DataUpdateConsumptionStartTime:
		eventType = "ohpcf.consumption_start_time_updated"
	case cemohpcf.DataUpdateRequestedPowerEstimate:
		eventType = "ohpcf.requested_power_estimate_updated"
	case cemohpcf.DataUpdateRequestedPowerMax:
		eventType = "ohpcf.requested_power_max_updated"
	case cemohpcf.DataUpdateMinimalRunDuration:
		eventType = "ohpcf.minimal_run_duration_updated"
	case cemohpcf.DataUpdateMinimalPauseDuration:
		eventType = "ohpcf.minimal_pause_duration_updated"
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
